// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// s3Access is what repo add knows of the keys of an s3: repository.
type s3Access struct {
	// content is the env file the keys the operator gave make; nil: the
	// env file in place is used as it is.
	content []byte
	// same: the env file in place holds these keys already.
	same bool
	// secrets are the assignments whose values no message may show.
	secrets []string
}

func (st *addState) isS3() bool { return st.s3 != nil }

// secretAssignments are the KEY=VALUE lines whose values are hidden in
// messages.
func (st *addState) secretAssignments() []string {
	if st.s3 == nil {
		return nil
	}
	return st.s3.secrets
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

// prepareS3 decides which keys the repository is tried with (Р29, Р30,
// Р45): the ones the operator gave, or, when they gave none and the env
// file in place holds the key id asked for, those. The secret key is read
// from its flag or asked at the terminal, here, before the locks.
func (c *hostCmd) prepareS3(st *addState) *refusal.Failure {
	inPlace, f := c.readEnv(c.layout.EnvFile(st.name))
	if f != nil {
		return f
	}
	if !c.givesSecretKey() && c.holdsKeys(inPlace) {
		st.s3 = &s3Access{same: true, secrets: secretAssignmentsOf(inPlace)}
		return nil
	}
	content, f := c.envFromOperator()
	if f != nil {
		return f
	}
	st.s3 = &s3Access{content: content, same: bytes.Equal(inPlace, content), secrets: secretAssignmentsOf(content)}
	return nil
}

// givesSecretKey: a flag names the source of the secret key.
func (c *hostCmd) givesSecretKey() bool {
	return c.opts.secretKeyStdin || c.opts.secretKeyFromFile != ""
}

// envFromOperator is the env file the operator's keys make: the secret
// key from its flag or the terminal.
func (c *hostCmd) envFromOperator() ([]byte, *refusal.Failure) {
	raw, f := c.readSource(secretKeySource(c.opts))
	if f != nil {
		return nil, f
	}
	key, f := hostsetup.S3SecretKey(raw)
	if f != nil {
		return nil, f
	}
	return hostsetup.S3EnvFile(c.opts.accessKeyID, key, c.opts.region), nil
}

// holdsKeys: the env file in place has the key id and the region asked for.
func (c *hostCmd) holdsKeys(inPlace []byte) bool {
	if inPlace == nil {
		return false
	}
	id, region := hostsetup.S3EnvIdentity(inPlace)
	return id == c.opts.accessKeyID && region == c.opts.region
}

// secretAssignmentsOf are the lines of an env file that hold a secret: all
// but the key id and the region, which are not.
func secretAssignmentsOf(env []byte) []string {
	assignments, _ := restic.ParseEnvFile(env)
	var secret []string
	for _, kv := range assignments {
		switch name, _, _ := bytes.Cut([]byte(kv), []byte("=")); string(name) {
		case "AWS_ACCESS_KEY_ID", "AWS_DEFAULT_REGION":
		default:
			secret = append(secret, kv)
		}
	}
	return secret
}

// readEnv reads the env file in place as root may: a plain file of the
// service user, never through a link (R4, ADR 0050). A missing file is
// nil; one that cannot be read as a secret is SECRET_FILE_REJECTED and is
// left as it is.
func (c *hostCmd) readEnv(path string) ([]byte, *refusal.Failure) {
	data, err := c.deps.readOwned(path, c.who.Service.UID)
	var secretErr *secrets.Error
	switch {
	case err == nil:
		return data, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case errors.As(err, &secretErr):
		return nil, refusal.Fail(refusal.SecretFileRejected, "%s: %v", refusal.SecretFileRejected, secretErr)
	}
	return nil, refusal.Fail(refusal.SecretFileRejected, "%s: %s cannot be read as a secret file: %v", refusal.SecretFileRejected, path, err)
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
