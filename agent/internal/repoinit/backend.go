// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/redact"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// ErrTimeout is the cause the command cancels its context with when
// --timeout runs out; any other cancellation is an interrupt.
var ErrTimeout = errors.New("timeout")

// Target is the repository a failure is about.
type Target struct {
	Name         string
	Backend      string
	PasswordFile string
	// Scrub removes secrets from text taken from restic.
	Scrub func(string) string
}

// Inspect asks restic whether the repository is initialised: its id if so.
func Inspect(ctx context.Context, r restic.Repository, t Target) (id string, initialized bool, f *refusal.Failure) {
	id, err := r.ID(ctx)
	switch {
	case err == nil:
		return id, true, nil
	case ctx.Err() == nil && errors.Is(err, restic.ErrNoRepository):
		return "", false, nil
	}
	return "", false, FromRestic(ctx, err, t)
}

// Create initialises the repository unless it exists (С10: restic cat
// config first, then init) and returns the new id.
func Create(ctx context.Context, r restic.Repository, t Target) (string, *refusal.Failure) {
	id, initialized, f := Inspect(ctx, r, t)
	if f != nil {
		return "", f
	}
	if initialized {
		return "", existsFailure(t, id)
	}
	id, err := r.Init(ctx)
	if err == nil {
		return id, nil
	}
	if ctx.Err() == nil && errors.Is(err, restic.ErrRepositoryExists) {
		existing, _ := r.ID(ctx) // a repository created in the meantime; its id is a courtesy
		return "", existsFailure(t, existing)
	}
	return "", withPartialNote(FromRestic(ctx, err, t))
}

func existsFailure(t Target, id string) *refusal.Failure {
	f := refusal.Fail(refusal.RepositoryExists, "a repository is already initialised at the address of %q; nothing was changed", t.Name)
	f.ID = id
	if id != "" {
		f.Detail += " (repository_id " + id + ")"
	}
	return f
}

// withPartialNote adds what an operator must know when init was stopped.
func withPartialNote(f *refusal.Failure) *refusal.Failure {
	if f.Reason == refusal.Timeout || f.Reason == refusal.Interrupted {
		f.Detail += "; the repository may have been created partially, run the command again"
	}
	return f
}

// resticReasons maps what restic said to a Reason; the first match wins.
var resticReasons = []struct {
	err    error
	reason refusal.Reason
}{
	{restic.ErrWrongPassword, refusal.WrongPassword},
	{restic.ErrEmptyPassword, refusal.PasswordFileEmpty},
	{restic.ErrNetwork, refusal.BackendUnavailable},
	{restic.ErrBadOutput, refusal.ResticOutputUnexpected},
}

// FromRestic explains an error of the restic wrapper. Text taken from
// restic goes through t.Scrub: it may echo the address or an env_file value.
func FromRestic(ctx context.Context, err error, t Target) *refusal.Failure {
	if f := fromContext(ctx); f != nil {
		f.Detail += " while working with the backend " + t.Backend
		return f
	}
	for _, r := range resticReasons {
		if errors.Is(err, r.err) {
			return describe(r.reason, err, t)
		}
	}
	return describe(refusal.BackendRefused, err, t)
}

func fromContext(ctx context.Context) *refusal.Failure {
	switch {
	case ctx.Err() == nil:
		return nil
	case errors.Is(context.Cause(ctx), ErrTimeout):
		return refusal.Fail(refusal.Timeout, "the command ran out of time (--timeout)")
	}
	return refusal.Fail(refusal.Interrupted, "the command was interrupted")
}

// Interruption reports how ctx ended: nil while it is running.
func Interruption(ctx context.Context) *refusal.Failure { return fromContext(ctx) }

func describe(reason refusal.Reason, err error, t Target) *refusal.Failure {
	cause := t.Scrub(causeOf(err))
	switch reason {
	case refusal.WrongPassword:
		return refusal.Fail(reason, "a repository already exists at the address of %q, and the password file %s does not open it", t.Name, t.PasswordFile)
	case refusal.PasswordFileEmpty:
		return refusal.Fail(reason, "restic refuses an empty password: the password file %s holds no password", t.PasswordFile)
	case refusal.BackendUnavailable:
		return refusal.Fail(reason, "backend %s: %s; the command can be repeated", t.Backend, cause)
	case refusal.ResticOutputUnexpected:
		return refusal.Fail(reason, "restic printed no repository id; check the repository with `sard-agent repo list`")
	}
	return refusal.Fail(reason, "backend %s: %s", t.Backend, cause)
}

// causeOf is restic's own fatal message if it printed one.
func causeOf(err error) string {
	var exit *restic.ExitError
	if !errors.As(err, &exit) || exit.Message == "" {
		return err.Error()
	}
	return exit.Cause()
}

// Scrubber returns a function that masks the secrets of a repository in
// text: the values of its env_file, the password in its address and the
// extra values (a password the command generated).
func Scrubber(repoURL string, envAssignments []string, extra ...string) func(string) string {
	var values [][]byte
	add := func(v string) {
		if v != "" {
			values = append(values, []byte(v))
		}
	}
	for _, kv := range envAssignments {
		_, v, _ := strings.Cut(kv, "=")
		add(v)
	}
	add(config.URLPassword(repoURL))
	for _, v := range extra {
		add(v)
	}
	return func(s string) string {
		var out bytes.Buffer
		w, err := redact.New(&out, values)
		if err != nil {
			return redact.Marker
		}
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return out.String()
	}
}
