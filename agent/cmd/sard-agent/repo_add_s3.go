// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

func (st *addState) isS3() bool { return st.s3 != nil }

// secretAssignments are the KEY=VALUE lines whose values are hidden in
// messages.
func (st *addState) secretAssignments() []string {
	if st.s3 == nil {
		return nil
	}
	return st.s3.Secrets
}

// secretKeySource are the flags that give the secret key of an s3: repository.
func secretKeySource(o hostOptions) hostsetup.SourceOptions {
	return hostsetup.SourceOptions{
		Stdin: o.secretKeyStdin, File: o.secretKeyFromFile,
		Flags:   hostsetup.SourceFlags{Stdin: "--secret-key-stdin", File: "--secret-key-from-file"},
		Subject: "the secret key of repository " + o.name,
	}
}

// checkAddSources refuses two sources at once, before any input is read
// (SECRET_SOURCE_CONFLICT): the password's, the secret key's, and standard
// input for both.
func checkAddSources(o hostOptions) *refusal.Failure {
	if f := hostsetup.CheckSources(passwordSource(o)); f != nil {
		return f
	}
	if f := hostsetup.CheckSources(secretKeySource(o)); f != nil {
		return f
	}
	if o.passwordStdin && o.secretKeyStdin {
		return refusal.Fail(refusal.SecretSourceConflict, "standard input can give one value only: use --password-from-file or --secret-key-from-file for one of the two")
	}
	return nil
}

// prepareS3 settles the keys of the repository (repoconnect.ChooseS3Env):
// the env file in place is read here, as root may, only through the
// owner-checked read (R4, ADR 0050), and the secret key is read from its
// flag or asked at the terminal, before the locks.
func (c *hostCmd) prepareS3(st *addState, plan hostsetup.AddPlan) *refusal.Failure {
	inPlace, _, f := repoconnect.ReadInPlace(c.owned(), c.layout.EnvFile(st.name))
	if f != nil {
		return f
	}
	if plan == hostsetup.AddUnchanged && !repoconnect.HoldsKeys(inPlace, c.opts.accessKeyID, c.opts.region) {
		if f := c.refusePasswordWithKeys(); f != nil {
			return f
		}
	}
	access, f := repoconnect.ChooseS3Env(inPlace, c.opts.accessKeyID, c.opts.region, c.givesSecretKey(),
		func() ([]byte, *refusal.Failure) { return c.readSource(secretKeySource(c.opts)) })
	if f != nil {
		return f
	}
	st.s3 = &access
	return nil
}

// owned is the read of a file of the service user that root may make.
func (c *hostCmd) owned() repoconnect.Owned {
	return func(path string) ([]byte, error) { return c.deps.readOwned(path, c.who.Service.UID) }
}

// givesSecretKey: a flag names the source of the secret key.
func (c *hostCmd) givesSecretKey() bool {
	return c.opts.secretKeyStdin || c.opts.secretKeyFromFile != ""
}

// finishRotation: the keys of a connected repository were replaced after
// restic accepted the new ones (Н17). The env file is read at each step,
// so the service is not restarted.
func (c *hostCmd) finishRotation(st *addState) int {
	c.record("repository", st.name, "credentials updated")
	_, _ = fmt.Fprintf(c.stdout, "Repository %q: credentials updated.\n  address:       %s\n  repository_id: %s\n  env file:      %s\nThe service reads the env file at every step: it is not restarted.\n",
		st.name, config.RedactURL(st.url), st.ID, c.layout.EnvFile(st.name))
	return exitOK
}

func (c *hostCmd) printS3Unchanged(st *addState, id string) {
	_, _ = fmt.Fprintf(c.stdout, "Repository %q unchanged: already connected to %s.\n  repository_id: %s\n  env file:      %s\n", st.name, config.RedactURL(st.url), id, c.layout.EnvFile(st.name))
	c.printKeyWarning(st.name)
}
