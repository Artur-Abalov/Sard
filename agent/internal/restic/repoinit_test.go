// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Golden: restic 0.19.1 init on a directory that already holds a
// repository exits with 1 and only this fatal message, whatever password
// it was given (checked with the pinned restic, A5b).
func TestInitOnAnExistingRepositoryIsRecognised(t *testing.T) {
	f := newFixture(t, map[string]reply{"init": {stderr: "init-exists.stderr", code: 1}})
	id, err := f.build().Init(context.Background())
	var exitErr *restic.ExitError
	if id != "" || !errors.Is(err, restic.ErrRepositoryExists) || !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("Init = %q, %v", id, err)
	}
}

// Golden: restic 0.19.1 refuses an empty password (a password file of only
// a line break is empty for restic) with exit code 1, for init and for
// every command that opens a repository.
func TestAnEmptyPasswordIsRecognised(t *testing.T) {
	init := newFixture(t, map[string]reply{"init": {stderr: "init-empty-password.stderr", code: 1}})
	if _, err := init.build().Init(context.Background()); !errors.Is(err, restic.ErrEmptyPassword) {
		t.Errorf("Init: %v", err)
	}
	cat := newFixture(t, map[string]reply{"cat": {stderr: "cat-config-empty-password.stderr", code: 1}})
	if _, err := cat.build().ID(context.Background()); !errors.Is(err, restic.ErrEmptyPassword) {
		t.Errorf("ID: %v", err)
	}
}

// Golden: an unreachable REST backend is retried by restic itself with
// backoff (about 15 minutes by default), so the "connection refused"
// arrives as a fatal error only long after the agent's own timeout. When
// restic does give up, the fatal message names the network cause.
func TestNetworkFailuresAreRecognised(t *testing.T) {
	for _, cause := range []string{
		"connection refused", "no such host", "i/o timeout", "network is unreachable",
		"no route to host", "connection reset by peer", "connection timed out", "TLS handshake timeout",
	} {
		f := newFixture(t, map[string]reply{"cat": {code: 1}})
		f.exec.fatal = "Fatal: unable to open config file: Stat: Head \"http://x/config\": dial tcp 127.0.0.1:9: connect: " + cause
		if _, err := f.build().ID(context.Background()); !errors.Is(err, restic.ErrNetwork) {
			t.Errorf("%s: %v", cause, err)
		}
	}
}

func TestOtherBackendFailuresAreNotNetworkFailures(t *testing.T) {
	for _, msg := range []string{
		"Fatal: The AWS Access Key Id you provided does not exist",
		"Fatal: Access Denied",
		"Fatal: tls: failed to verify certificate: x509: certificate signed by unknown authority",
		"Fatal: permission denied",
	} {
		f := newFixture(t, map[string]reply{"cat": {code: 1}})
		f.exec.fatal = msg
		_, err := f.build().ID(context.Background())
		var exitErr *restic.ExitError
		if errors.Is(err, restic.ErrNetwork) || !errors.As(err, &exitErr) || exitErr.Message != msg {
			t.Errorf("%q: %v", msg, err)
		}
	}
}
