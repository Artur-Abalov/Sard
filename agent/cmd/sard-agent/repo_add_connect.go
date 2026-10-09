// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// reportUnchanged is the repeat of a command that already succeeded
// (Р11): the repository is asked for its id with the files of the fragment,
// nothing is written. False means it is not there any more, and the
// command goes on as for a new one; otherwise code is the command's.
func (c *hostCmd) reportUnchanged(ctx context.Context, st *addState) (code int, done bool) {
	repo := c.repository(st.name)
	var log repoconnect.Log
	target, cli := c.resticFor(st, repoconnect.Files{Password: repo.PasswordFile, Env: repo.EnvFile}, &log)
	id, initialized, f := c.bound().Inspect(ctx, cli, target, &log)
	if f != nil {
		return c.fail(f), true
	}
	if !initialized {
		return exitOK, false
	}
	c.printUnchanged(st, id)
	return exitOK, true
}

// bound is the limit of the first access to the storage (Р33).
func (c *hostCmd) bound() repoconnect.Bound {
	return repoconnect.Bound{Clock: c.deps.clock, Timeout: c.opts.connectTimeout}
}

func (c *hostCmd) printUnchanged(st *addState, id string) {
	if st.isS3() {
		c.printS3Unchanged(st, id)
		return
	}
	_, _ = fmt.Fprintf(c.stdout, "Repository %q unchanged: already connected to %s.\n  repository_id: %s\n", st.name, config.RedactURL(st.url), id)
	c.printKeyWarning(st.name)
}

func (c *hostCmd) printKeyWarning(name string) {
	_, _ = fmt.Fprintf(c.stdout, "\nWARNING: the key of this repository exists only on this host, in %s.\n", c.layout.PasswordFile(name))
	_, _ = fmt.Fprintf(c.stdout, "Save a copy outside this host now: without it the backups are unrecoverable if this host is lost.\n  sudo sard-agent repo password %s --reveal\n", name)
}

// addLocked takes the config lock, then the init lock of the name (Р10),
// looks at the config once more and connects the repository.
func (c *hostCmd) addLocked(ctx context.Context, st *addState) int {
	return c.underConfigLock(
		func() (f *refusal.Failure) {
			_, f = hostsetup.PlanRepository(c.cfg, c.layout, st.name, st.url)
			return f
		},
		func() int { return c.underInitLock(ctx, st) })
}

func (c *hostCmd) underInitLock(ctx context.Context, st *addState) int {
	unlockInit, f := repoinit.AcquireLock(c.deps.openLock, cacheDir(c.cfg, c.deps), config.Repository{Name: st.name})
	if f != nil {
		return c.fail(f)
	}
	defer unlockInit()
	return c.connectAndWrite(ctx, st)
}

func (c *hostCmd) connectAndWrite(ctx context.Context, st *addState) int {
	if f := c.prepareStorage(ctx, st); f != nil {
		return c.fail(f)
	}
	if code, done := c.sftpRepeat(ctx, st); done {
		return code
	}
	if f := c.connect(ctx, st); f != nil {
		return c.failConnect(ctx, st, f)
	}
	if code, done := c.finishConnected(st); done {
		return code
	}
	if f := c.writeRepository(st); f != nil {
		return c.fail(f)
	}
	c.record("repository", st.name, "added")
	c.printAdded(st)
	return c.finish()
}

// finishConnected ends the command for a repository that was connected
// already and only its keys (Н17) or its ssh files (Р43) were set up again.
func (c *hostCmd) finishConnected(st *addState) (int, bool) {
	switch {
	case st.rotation && st.Attached:
		return c.finishRotation(st), true
	case st.isSFTP() && st.plan == hostsetup.AddUnchanged && st.Attached:
		return c.finishSSHUpdate(st), true
	}
	return exitOK, false
}

// prepareStorage makes ready what the storage needs on this host: the
// directory of a local repository, the ssh side of an sftp: one; an s3:
// storage needs nothing.
func (c *hostCmd) prepareStorage(ctx context.Context, st *addState) *refusal.Failure {
	switch {
	case st.isS3():
		return nil
	case st.isSFTP():
		return c.setupSSH(ctx, st)
	}
	if err := c.prepareRepositoryDir(st.url); err != nil {
		return writeFailed(err)
	}
	return nil
}

// failConnect reports a failure of the backend; the files the command
// left are named, whatever the class of the refusal: the command used
// them, a repeat will too (Р16, П12). An interrupt or a timeout says it
// in its own words.
func (c *hostCmd) failConnect(ctx context.Context, st *addState, f *refusal.Failure) int {
	c.noteSSHFilesStay(st)
	if c.exists(st.final(c)) && repoinit.Interruption(ctx) == nil {
		_, _ = fmt.Fprintf(c.stderr, "sard-agent repo add: %s will be used when the command is repeated\n", c.keptFiles(st))
	}
	return c.fail(f)
}

// keptFiles names the files a failed command left, for the operator.
func (c *hostCmd) keptFiles(st *addState) string {
	if st.isS3() {
		return fmt.Sprintf("the files %s and %s were kept and", c.layout.EnvFile(st.name), st.final(c))
	}
	return fmt.Sprintf("the password file %s was kept and", st.final(c))
}

func (c *hostCmd) exists(path string) bool {
	_, err := c.deps.fs.Stat(path)
	return err == nil
}

// prepareRepositoryDir makes the directory of a local repository ready
// for the service user (Р13): missing parents are root's, 0755; the
// repository directory itself is the service user's, 0700; an existing
// one gets the service user as owner, recursively, modes untouched. The
// path is walked without ever following a symbolic link (R2, ADR 0050),
// so neither the creation nor the change of owner can be sent elsewhere.
func (c *hostCmd) prepareRepositoryDir(path string) error {
	d, created, err := hostsetup.OpenDir(c.deps.fs, path, &hostsetup.Make{Parents: hostsetup.Attrs{Mode: 0o755}, Last: c.serviceOwner(0o700)})
	if err != nil {
		return err
	}
	_ = d.Close()
	if slices.Contains(created, path) {
		return nil
	}
	return hostsetup.ChownTree(c.deps.fs, path, int(c.who.Service.UID), int(c.who.Service.GID))
}

// connect finds out whether the repository exists and either attaches it
// or creates it (С10 of repo-init.feature).
func (c *hostCmd) connect(ctx context.Context, st *addState) *refusal.Failure {
	return c.connector(st).Connect(ctx)
}

// connector is the connection of the repository of st to this host.
func (c *hostCmd) connector(st *addState) *repoconnect.Connector {
	conn := &repoconnect.Connector{
		FS:         c.deps.fs,
		Random:     c.deps.random,
		SecretsDir: c.layout.SecretsDir(),
		Final:      st.final(c),
		Owner:      c.serviceOwner,
		Open: func(files repoconnect.Files, stderr io.Writer) (repoinit.Target, restic.Repository) {
			return c.resticFor(st, files, stderr)
		},
		AskPassword: func() ([]byte, *refusal.Failure) { return c.readSource(passwordSource(c.opts)) },
		Bound:       c.bound(),
		KeysOnly:    st.rotation,
		Owned:       c.owned(),
		State:       &st.State,
	}
	if st.isS3() {
		conn.Env = &repoconnect.EnvFile{Final: c.layout.EnvFile(st.name), Content: st.s3.Content}
	}
	return conn
}

// writeRepository writes the drop-in (on a systemd host, for a local path)
// and the fragment, the fragment last (Р4).
func (c *hostCmd) writeRepository(st *addState) *refusal.Failure {
	if f := c.writeDropIn(st); f != nil {
		return f
	}
	body := hostsetup.RepositoryYAML(st.name, st.url, st.final(c))
	if st.isS3() {
		body = hostsetup.RepositoryYAMLWithEnv(st.name, st.url, st.final(c), c.layout.EnvFile(st.name))
	}
	if err := hostsetup.WriteFile(c.deps.fs, c.layout.RepositoryFragment(st.name), body, c.fragmentOwner()); err != nil {
		return writeFailed(err)
	}
	return nil
}

// writeDropIn lets the service write to a local repository (Р13).
func (c *hostCmd) writeDropIn(st *addState) *refusal.Failure {
	if !st.isLocal() || !c.deps.systemd.Present() {
		return nil
	}
	dropIn := hostsetup.DropIn{FS: c.deps.fs, Dir: c.deps.dropInDir}
	if err := dropIn.Write(st.name, st.url); err != nil {
		return writeFailed(err)
	}
	return hostsetup.Reload(c.deps.systemd)
}

func (c *hostCmd) printAdded(st *addState) {
	what := "Result: created a new repository."
	if st.Attached {
		what = "Result: attached an existing repository."
	}
	backend, files := "local", "  password file: "+st.final(c)+generatedNote(st)+"\n"
	switch {
	case st.isS3():
		backend, files = "s3", "  env file:      "+c.layout.EnvFile(st.name)+"\n"+files
	case st.isSFTP():
		backend = "sftp"
	}
	_, _ = fmt.Fprintf(c.stdout, "Repository %q added.\n  backend:       %s\n  address:       %s\n  repository_id: %s\n%s",
		st.name, backend, config.RedactURL(st.url), st.ID, files)
	if st.isSFTP() {
		c.printSFTPAdded(st)
	}
	_, _ = fmt.Fprintf(c.stdout, "%s\n", what)
	c.printKeyWarning(st.name)
	if st.isLocal() {
		_, _ = fmt.Fprintln(c.stdout, "\nWARNING: this repository is on this host: its backups are lost together with this host. Add a repository on another host or in cloud storage as well.")
	}
}

// generatedNote says so when the command made the password up.
func generatedNote(st *addState) string {
	if st.Generated != "" {
		return " (generated by this command)"
	}
	return ""
}
