// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// reportUnchanged is the repeat of a command that already succeeded
// (Р11): the repository is asked for its id with the password file of the
// fragment, nothing is written. False means it is not there any more, and
// the command goes on as for a new one.
func (c *hostCmd) reportUnchanged(ctx context.Context, st *addState) bool {
	repo := c.repository(st.name)
	target, cli := c.resticFor(st, repo.PasswordFile)
	id, initialized, f := repoinit.Inspect(ctx, cli, target)
	if f != nil {
		c.fail(f)
		return true
	}
	if !initialized {
		return false
	}
	_, _ = fmt.Fprintf(c.stdout, "Repository %q unchanged: already connected to %s.\n  repository_id: %s\n", st.name, st.url, id)
	c.printKeyWarning(st.name)
	return true
}

func (c *hostCmd) printKeyWarning(name string) {
	_, _ = fmt.Fprintf(c.stdout, "\nWARNING: the key of this repository exists only on this host, in %s.\n", c.layout.PasswordFile(name))
	_, _ = fmt.Fprintf(c.stdout, "Save a copy outside this host now: without it the backups are unrecoverable if this host is lost.\n  sudo sard-agent repo password %s --reveal\n", name)
}

// addLocked takes the config lock, then the init lock of the name (Р10),
// looks at the config once more and connects the repository.
func (c *hostCmd) addLocked(ctx context.Context, st *addState) int {
	unlock, f := c.lock()
	if f != nil {
		return c.fail(f)
	}
	defer unlock()
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	if _, f := c.planRepository(st.name, st.url); f != nil {
		return c.fail(f)
	}
	unlockInit, f := repoinit.AcquireLock(c.deps.openLock, cacheDir(c.cfg, c.deps), config.Repository{Name: st.name})
	if f != nil {
		return c.fail(f)
	}
	defer unlockInit()
	return c.connectAndWrite(ctx, st)
}

func (c *hostCmd) connectAndWrite(ctx context.Context, st *addState) int {
	if err := c.prepareRepositoryDir(st.url); err != nil {
		return c.fail(writeFailed(err))
	}
	if f := c.connect(ctx, st); f != nil {
		return c.failConnect(ctx, st, f)
	}
	if f := c.writeRepository(st); f != nil {
		return c.fail(f)
	}
	c.record("repository", st.name, "added")
	c.printAdded(st)
	return c.finish()
}

// failConnect reports a failure of the backend; the password file the
// command left is named: the command used it, a repeat will too (Р16).
func (c *hostCmd) failConnect(ctx context.Context, st *addState, f *repoinit.Failure) int {
	if _, err := c.deps.fs.Stat(st.final(c)); err == nil && repoinit.Interruption(ctx) == nil && f.Class == repoinit.ClassAgentError {
		_, _ = fmt.Fprintf(c.stderr, "sard-agent repo add: the password file %s was kept and will be used when the command is repeated\n", st.final(c))
	}
	return c.fail(f)
}

// prepareRepositoryDir makes the directory of a local repository ready
// for the service user (Р13): missing parents are root's, 0755; the
// repository directory itself is the service user's, 0700; an existing
// one gets the service user as owner, recursively, modes untouched.
func (c *hostCmd) prepareRepositoryDir(path string) error {
	if _, err := c.deps.fs.Stat(path); err == nil {
		return hostsetup.ChownTree(c.deps.fs, path, int(c.who.Service.UID), int(c.who.Service.GID))
	}
	missing := c.missingDirs(path)
	for i, dir := range missing {
		attrs := hostsetup.Attrs{Mode: 0o755}
		if i == len(missing)-1 {
			attrs = c.serviceOwner(0o700)
		}
		if _, err := hostsetup.EnsureDir(c.deps.fs, dir, attrs); err != nil {
			return err
		}
	}
	return nil
}

// missingDirs lists the directories of path that do not exist, outermost first.
func (c *hostCmd) missingDirs(path string) []string {
	var missing []string
	for dir := path; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := c.deps.fs.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			break
		}
		missing = append([]string{dir}, missing...)
	}
	return missing
}

// candidate is a password file the repository is tried with.
type candidate struct {
	// path is what restic gets.
	path string
	// staged is the temporary file that becomes the password file when the
	// repository accepts it; empty for the file already in place.
	staged string
	// given: the operator chose this password, so a refusal is final.
	given bool
}

// firstCandidate is the password to try first: the one given, else the
// file a failed command left, else a new one (Р15, Р16).
func (c *hostCmd) firstCandidate(st *addState) (candidate, *repoinit.Failure) {
	if _, err := hostsetup.EnsureDir(c.deps.fs, c.layout.SecretsDir(), c.serviceOwner(0o700)); err != nil {
		return candidate{}, writeFailed(err)
	}
	switch {
	case st.provided != nil:
		return c.stage(st, st.provided, true)
	case c.exists(st.final(c)):
		return candidate{path: st.final(c)}, nil
	}
	password, err := repoinit.NewPassword(c.deps.random)
	if err != nil {
		return candidate{}, repoinit.Fail(repoinit.PasswordFileWrite, "%v", err)
	}
	st.generated = password
	return c.stage(st, []byte(password+"\n"), false)
}

func (c *hostCmd) exists(path string) bool {
	_, err := c.deps.fs.Stat(path)
	return err == nil
}

// stage writes the password to a temporary file in the secrets directory.
func (c *hostCmd) stage(st *addState, password []byte, given bool) (candidate, *repoinit.Failure) {
	tmp, err := hostsetup.StageFile(c.deps.fs, st.final(c), password, c.serviceOwner(0o600))
	if err != nil {
		return candidate{}, writeFailed(err)
	}
	return candidate{path: tmp, staged: tmp, given: given}, nil
}

// commit makes the candidate the password file.
func (c *hostCmd) commit(st *addState, cand candidate) *repoinit.Failure {
	if cand.staged == "" {
		return nil
	}
	if err := hostsetup.CommitFile(c.deps.fs, cand.staged, st.final(c)); err != nil {
		return writeFailed(err)
	}
	return nil
}

func (c *hostCmd) discard(cand candidate) {
	if cand.staged != "" {
		hostsetup.DiscardFile(c.deps.fs, cand.staged)
	}
}

// outcome of trying a candidate.
type outcome struct {
	f *repoinit.Failure
	// needPassword: the repository exists and the candidate does not open it.
	needPassword bool
}

// connect finds out whether the repository exists and either attaches it
// or creates it (С10 of repo-init.feature).
func (c *hostCmd) connect(ctx context.Context, st *addState) *repoinit.Failure {
	cand, f := c.firstCandidate(st)
	if f != nil {
		return f
	}
	out := c.try(ctx, st, cand)
	if !out.needPassword {
		return out.f
	}
	password, f := c.readSource(passwordSource(c.opts))
	if f != nil {
		return f
	}
	st.provided = password
	if cand, f = c.stage(st, password, true); f != nil {
		return f
	}
	return c.try(ctx, st, cand).f
}

// try opens the repository with the candidate: it is attached if it is
// there, created if it is not.
func (c *hostCmd) try(ctx context.Context, st *addState, cand candidate) outcome {
	target, cli := c.resticFor(st, cand.path)
	id, initialized, f := repoinit.Inspect(ctx, cli, target)
	switch {
	case f != nil && f.Reason == repoinit.WrongPassword && !cand.given:
		c.discard(cand)
		return outcome{needPassword: true}
	case f != nil:
		c.discard(cand)
		return outcome{f: f}
	}
	if f := c.commit(st, cand); f != nil {
		return outcome{f: f}
	}
	if initialized {
		st.id, st.attached = id, true
		return outcome{}
	}
	return outcome{f: c.create(ctx, st)}
}

// create makes the repository with the password file now in place.
func (c *hostCmd) create(ctx context.Context, st *addState) *repoinit.Failure {
	target, cli := c.resticFor(st, st.final(c))
	id, f := repoinit.Create(ctx, cli, target)
	st.id = id
	return f
}

// writeRepository writes the drop-in (on a systemd host) and the fragment,
// the fragment last (Р4).
func (c *hostCmd) writeRepository(st *addState) *repoinit.Failure {
	if c.deps.systemd.Present() {
		dropIn := hostsetup.DropIn{FS: c.deps.fs, Dir: c.deps.dropInDir}
		if err := dropIn.Write(st.name, st.url); err != nil {
			return writeFailed(err)
		}
		if f := hostsetup.Reload(c.deps.systemd); f != nil {
			return f
		}
	}
	body := hostsetup.RepositoryYAML(st.name, st.url, st.final(c))
	if err := hostsetup.WriteFile(c.deps.fs, c.layout.RepositoryFragment(st.name), body, c.fragmentOwner()); err != nil {
		return writeFailed(err)
	}
	return nil
}

func (c *hostCmd) printAdded(st *addState) {
	what := "Result: created a new repository."
	if st.attached {
		what = "Result: attached an existing repository."
	}
	_, _ = fmt.Fprintf(c.stdout, "Repository %q added.\n  backend:       local\n  address:       %s\n  repository_id: %s\n  password file: %s%s\n%s\n",
		st.name, st.url, st.id, st.final(c), generatedNote(st), what)
	c.printKeyWarning(st.name)
	_, _ = fmt.Fprintln(c.stdout, "\nWARNING: this repository is on this host: its backups are lost together with this host. Add a repository on another host or in cloud storage as well.")
}

// generatedNote says so when the command made the password up.
func generatedNote(st *addState) string {
	if st.generated != "" {
		return " (generated by this command)"
	}
	return ""
}
