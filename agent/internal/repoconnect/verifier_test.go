// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// V3 (K2): on a verifier the check of the lock is read the other way round: a
// storage that refuses the lock is what the verifier is given.

func openedWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	w.conn.AskPassword = func() ([]byte, *refusal.Failure) { return []byte("asked\n"), nil }
	return w
}

var errForbidden = fmt.Errorf("restic snapshots: %w", &restic.ExitError{Code: 1, Message: "Fatal: unable to create lock in backend: client.PutObject: Forbidden"})

func TestAVerifierAcceptsAStorageThatRefusesTheLock(t *testing.T) {
	w := openedWorld(t)
	w.conn.Verifier = true
	w.repo.lockErr = errForbidden
	if f := w.conn.Connect(context.Background()); f != nil {
		t.Fatalf("Connect = %v", f)
	}
	if w.conn.CanWrite {
		t.Error("read-only credentials are taken for credentials that can write")
	}
}

func TestAVerifierIsToldThatItsCredentialsCanWrite(t *testing.T) {
	w := openedWorld(t)
	w.conn.Verifier = true
	if f := w.conn.Connect(context.Background()); f != nil {
		t.Fatalf("Connect = %v", f)
	}
	if !w.conn.CanWrite {
		t.Error("credentials that take the lock are not reported as able to write")
	}
}

func TestASourceStillRefusesAStorageThatRefusesTheLock(t *testing.T) {
	w := openedWorld(t)
	w.repo.lockErr = errForbidden
	f := w.conn.Connect(context.Background())
	if f == nil || f.Reason != refusal.StorageAccessDenied {
		t.Fatalf("Connect = %v", f)
	}
	if strings.Contains(f.Detail, "verifier token") {
		t.Errorf("a source is sent to register: %s", f.Detail)
	}
}

func TestTheLockHintIsAddedToTheRefusalOfASource(t *testing.T) {
	w := openedWorld(t)
	w.conn.LockHint = "register this host first with a verifier token"
	w.repo.lockErr = errForbidden
	f := w.conn.Connect(context.Background())
	if f == nil || f.Reason != refusal.StorageAccessDenied || !strings.Contains(f.Detail, "register this host first with a verifier token") {
		t.Fatalf("Connect = %v", f)
	}
}

func TestAVerifierStillRefusesAStorageThatIsUnreachable(t *testing.T) {
	w := openedWorld(t)
	w.conn.Verifier = true
	w.repo.lockErr = fmt.Errorf("restic snapshots: %w", restic.ErrNetwork)
	if f := w.conn.Connect(context.Background()); f == nil {
		t.Fatal("an unreachable storage is accepted")
	}
}
