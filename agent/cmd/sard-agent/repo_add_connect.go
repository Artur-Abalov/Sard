// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
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
	c.printUnchanged(st, id)
	return true
}

func (c *hostCmd) printUnchanged(st *addState, id string) {
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
func (c *hostCmd) failConnect(ctx context.Context, st *addState, f *refusal.Failure) int {
	if _, err := c.deps.fs.Stat(st.final(c)); err == nil && repoinit.Interruption(ctx) == nil && f.Class == refusal.ClassAgentError {
		_, _ = fmt.Fprintf(c.stderr, "sard-agent repo add: the password file %s was kept and will be used when the command is repeated\n", st.final(c))
	}
	return c.fail(f)
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
	return &repoconnect.Connector{
		FS:         c.deps.fs,
		Random:     c.deps.random,
		SecretsDir: c.layout.SecretsDir(),
		Final:      st.final(c),
		Owner:      c.serviceOwner,
		Open: func(passwordFile string) (repoinit.Target, restic.Repository) {
			return c.resticFor(st, passwordFile)
		},
		AskPassword: func() ([]byte, *refusal.Failure) { return c.readSource(passwordSource(c.opts)) },
		State:       &st.State,
	}
}

// writeRepository writes the drop-in (on a systemd host) and the fragment,
// the fragment last (Р4).
func (c *hostCmd) writeRepository(st *addState) *refusal.Failure {
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
	if st.Attached {
		what = "Result: attached an existing repository."
	}
	_, _ = fmt.Fprintf(c.stdout, "Repository %q added.\n  backend:       local\n  address:       %s\n  repository_id: %s\n  password file: %s%s\n%s\n",
		st.name, config.RedactURL(st.url), st.ID, st.final(c), generatedNote(st), what)
	c.printKeyWarning(st.name)
	_, _ = fmt.Fprintln(c.stdout, "\nWARNING: this repository is on this host: its backups are lost together with this host. Add a repository on another host or in cloud storage as well.")
}

// generatedNote says so when the command made the password up.
func generatedNote(st *addState) string {
	if st.Generated != "" {
		return " (generated by this command)"
	}
	return ""
}
