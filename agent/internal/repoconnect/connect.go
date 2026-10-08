// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"context"
	"io"
	"os"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// State is what the connection learns and the command reports afterwards.
type State struct {
	// Provided is the password the operator gave (nil: none yet).
	Provided []byte
	// Generated is the password the connection made up.
	Generated string
	// ID is the repository id once the repository is attached or created.
	ID string
	// Attached: the repository existed, it was not created.
	Attached bool
}

// Files are the files restic gets to open the repository.
type Files struct {
	// Password is the password file.
	Password string
	// Env is the env file of the backend; empty for none.
	Env string
}

// EnvFile is the env file of an S3 repository (Р30).
type EnvFile struct {
	// Final is where the env file lives.
	Final string
	// Content is the env file the operator's keys make. Nil: the file in
	// place is used as it is.
	Content []byte
}

// Connector connects one repository.
type Connector struct {
	FS hostsetup.FS
	// Random is the source of generated passwords.
	Random io.Reader
	// SecretsDir holds the password file; it is made if missing.
	SecretsDir string
	// Final is the password file of the repository.
	Final string
	// Env is the env file of the repository, nil if it has none.
	Env *EnvFile
	// Owner gives the owner of what the service must be able to use, with
	// the mode asked for.
	Owner func(mode os.FileMode) hostsetup.Attrs
	// Open is restic for the repository opened with the files, writing
	// its stderr also to the writer, and the target its failures are
	// described for.
	Open func(files Files, stderr io.Writer) (repoinit.Target, restic.Repository)
	// AskPassword gives the password of an existing repository: from a
	// flag, or from the terminal.
	AskPassword func() ([]byte, *refusal.Failure)
	// Bound limits the first access to the storage.
	Bound Bound
	// KeysOnly: the repository is connected already and only the keys in
	// the env file change (Н17). The password file in place is the only
	// candidate and is never written, a wrong password is final and never
	// asked again, a repository that is not there is a refusal, and init
	// never runs.
	KeysOnly bool
	// Owned reads a file in place as the service user owns it. A leftover
	// password file is judged by it like the env file (O1); nil: its
	// existence is all that is looked at.
	Owned Owned
	*State

	// env is the env file candidate; accessed: the first access is made.
	env      candidate
	accessed bool
}

// candidate is a file the repository is tried with.
type candidate struct {
	// path is what restic gets.
	path string
	// staged is the temporary file that becomes the final file when the
	// repository accepts it; empty for the file already in place.
	staged string
	// given: the operator chose this password, so a refusal is final.
	given bool
}

// stageEnv writes the keys to a temporary file in the secrets directory,
// with the owner and the mode before the content; without keys the env
// file in place is the candidate.
func (c *Connector) stageEnv() *refusal.Failure {
	if c.Env == nil {
		return nil
	}
	if c.Env.Content == nil {
		c.env = candidate{path: c.Env.Final}
		return nil
	}
	if _, err := hostsetup.EnsureDir(c.FS, c.SecretsDir, c.Owner(0o700)); err != nil {
		return writeFailed(err)
	}
	tmp, err := hostsetup.StageFile(c.FS, c.Env.Final, c.Env.Content, c.Owner(0o600))
	if err != nil {
		return writeFailed(err)
	}
	c.env = candidate{path: tmp, staged: tmp}
	return nil
}

// firstCandidate is the password to try first: the one given, else the
// file a failed command left, else a new one (Р15, Р16).
func (c *Connector) firstCandidate() (candidate, *refusal.Failure) {
	if _, err := hostsetup.EnsureDir(c.FS, c.SecretsDir, c.Owner(0o700)); err != nil {
		return candidate{}, writeFailed(err)
	}
	if c.Provided != nil {
		return c.stage(c.Provided, true)
	}
	found, f := c.inPlace(c.Final)
	switch {
	case f != nil:
		return candidate{}, f
	case found:
		return candidate{path: c.Final}, nil
	}
	return c.generated()
}

// generated is a new password, made up by the command.
func (c *Connector) generated() (candidate, *refusal.Failure) {
	password, err := repoinit.NewPassword(c.Random)
	if err != nil {
		return candidate{}, refusal.Fail(refusal.PasswordFileWrite, "%v", err)
	}
	c.Generated = password
	return c.stage([]byte(password+"\n"), false)
}

// inPlace says whether the file is there; one that cannot be read as a
// secret of the service user is SECRET_FILE_REJECTED.
func (c *Connector) inPlace(path string) (bool, *refusal.Failure) {
	if c.Owned != nil {
		_, found, f := ReadInPlace(c.Owned, path)
		return found, f
	}
	_, err := c.FS.Stat(path)
	return err == nil, nil
}

// stage writes the password to a temporary file in the secrets directory.
func (c *Connector) stage(password []byte, given bool) (candidate, *refusal.Failure) {
	tmp, err := hostsetup.StageFile(c.FS, c.Final, password, c.Owner(0o600))
	if err != nil {
		return candidate{}, writeFailed(err)
	}
	return candidate{path: tmp, staged: tmp, given: given}, nil
}

// commit makes the candidates the env file and the password file: the env
// file first, so that a failure of it leaves no password file behind.
func (c *Connector) commit(cand candidate) *refusal.Failure {
	if c.env.staged != "" {
		if err := hostsetup.CommitFile(c.FS, c.env.staged, c.Env.Final); err != nil {
			return writeFailed(err)
		}
		c.env.staged = ""
	}
	if cand.staged == "" {
		return nil
	}
	if err := hostsetup.CommitFile(c.FS, cand.staged, c.Final); err != nil {
		return writeFailed(err)
	}
	return nil
}

func (c *Connector) discard(cand candidate) {
	if cand.staged != "" {
		hostsetup.DiscardFile(c.FS, cand.staged)
	}
}

// outcome of trying a candidate.
type outcome struct {
	f *refusal.Failure
	// needPassword: the repository exists and the candidate does not open it.
	needPassword bool
}

// Connect finds out whether the repository exists and either attaches it
// or creates it (С10 of repo-init.feature).
func (c *Connector) Connect(ctx context.Context) *refusal.Failure {
	if f := c.stageEnv(); f != nil {
		return f
	}
	defer c.discardEnv()
	if c.KeysOnly {
		return c.connectKeysOnly(ctx)
	}
	cand, f := c.firstCandidate()
	if f != nil {
		return f
	}
	if out := c.try(ctx, cand); !out.needPassword {
		return out.f
	}
	return c.tryAskedPassword(ctx)
}

// connectKeysOnly tries the new keys with the password file in place.
func (c *Connector) connectKeysOnly(ctx context.Context) *refusal.Failure {
	found, f := c.inPlace(c.Final)
	if f != nil {
		return f
	}
	if !found {
		return refusal.Fail(refusal.PasswordFileMissing, "the password file %s of the connected repository is missing; nothing was changed", c.Final)
	}
	id, initialized, f := c.inspect(ctx, Files{Password: c.Final, Env: c.env.path})
	switch {
	case f != nil:
		return f
	case !initialized:
		return refusal.Fail(refusal.RepositoryConflict, "the new keys see no repository at the address; a connected repository is never created again by a change of keys, nothing was changed")
	}
	if f := c.commit(candidate{}); f != nil {
		return f
	}
	c.ID, c.Attached = id, true
	return nil
}

// tryAskedPassword: the repository exists and the password nobody chose
// does not open it; the operator gives one.
func (c *Connector) tryAskedPassword(ctx context.Context) *refusal.Failure {
	password, f := c.AskPassword()
	if f != nil {
		return f
	}
	c.Provided = password
	cand, f := c.stage(password, true)
	if f != nil {
		return f
	}
	return c.try(ctx, cand).f
}

// discardEnv gives up the staged env file, if it was not committed.
func (c *Connector) discardEnv() { c.discard(c.env) }

// try opens the repository with the candidate: it is attached if it is
// there, created if it is not.
func (c *Connector) try(ctx context.Context, cand candidate) outcome {
	id, initialized, f := c.inspect(ctx, Files{Password: cand.path, Env: c.env.path})
	if f != nil {
		return c.rejected(cand, f)
	}
	return c.accepted(ctx, cand, id, initialized)
}

// inspect asks whether the repository is there; the first time within the
// time the storage is given to answer (Р33).
func (c *Connector) inspect(ctx context.Context, files Files) (string, bool, *refusal.Failure) {
	var log Log
	target, cli := c.Open(files, &log)
	if c.accessed {
		return repoinit.Inspect(ctx, cli, target)
	}
	c.accessed = true
	return c.Bound.Inspect(ctx, cli, target, &log)
}

// rejected: the repository could not be opened with the candidate. A
// password nobody chose that does not open an existing repository means
// the operator has to give one.
func (c *Connector) rejected(cand candidate, f *refusal.Failure) outcome {
	c.discard(cand)
	if f.Reason == refusal.WrongPassword && !cand.given {
		return outcome{needPassword: true}
	}
	return outcome{f: f}
}

// accepted: the candidate becomes the password file; the repository is
// attached if it was there, created if it was not.
func (c *Connector) accepted(ctx context.Context, cand candidate, id string, initialized bool) outcome {
	if f := c.commit(cand); f != nil {
		c.discard(cand)
		return outcome{f: f}
	}
	if initialized {
		c.ID, c.Attached = id, true
		return outcome{}
	}
	return outcome{f: c.create(ctx)}
}

// create makes the repository with the password file now in place.
func (c *Connector) create(ctx context.Context) *refusal.Failure {
	target, cli := c.Open(Files{Password: c.Final, Env: c.envFinal()}, nil)
	id, f := repoinit.Create(ctx, cli, target)
	c.ID = id
	return f
}

// envFinal is the env file restic gets once the files are in place.
func (c *Connector) envFinal() string {
	if c.Env == nil {
		return ""
	}
	return c.Env.Final
}

// writeFailed is CONFIG_WRITE: the message names the file or directory.
func writeFailed(err error) *refusal.Failure {
	return refusal.Fail(refusal.ConfigWrite, "%v", err)
}
