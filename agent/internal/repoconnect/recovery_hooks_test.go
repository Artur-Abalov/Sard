// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// V3 (K15): the command that connects a repository asks before a new one is
// made, and is told when it was made, with the password file in place.

func TestBeforeCreateStopsTheConnectionOfANewRepositoryBeforeAnythingIsWritten(t *testing.T) {
	w := newWorld(t)
	w.conn.BeforeCreate = func() *refusal.Failure { return refusal.Fail(refusal.RecoveryKeyOutputMissing, "no output") }
	f := w.conn.Connect(context.Background())
	if f == nil || f.Reason != refusal.RecoveryKeyOutputMissing {
		t.Fatalf("Connect = %v", f)
	}
	if w.repo.inits != 0 {
		t.Error("the repository was created")
	}
	if left := w.leftovers(); len(left) != 0 {
		t.Errorf("files left: %v", left)
	}
}

func TestBeforeCreateIsNotAskedForARepositoryThatExists(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	asked := false
	w.conn.BeforeCreate = func() *refusal.Failure { asked = true; return nil }
	if f := w.conn.Connect(context.Background()); f != nil {
		t.Fatal(f)
	}
	if asked {
		t.Error("BeforeCreate was called for an existing repository")
	}
}

func TestCreatedIsCalledAfterInitWithThePasswordFileInPlace(t *testing.T) {
	w := newWorld(t)
	var seen string
	var inits int
	w.conn.Created = func(context.Context) {
		inits = w.repo.inits
		seen = w.content(w.conn.Final)
	}
	if f := w.conn.Connect(context.Background()); f != nil {
		t.Fatal(f)
	}
	if inits != 1 || seen == "" {
		t.Errorf("inits %d, password file %q when Created ran", inits, seen)
	}
}

func TestCreatedIsNotCalledForAnExistingRepository(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	w.conn.Created = func(context.Context) { t.Error("Created was called for an existing repository") }
	if f := w.conn.Connect(context.Background()); f != nil {
		t.Fatal(f)
	}
}
