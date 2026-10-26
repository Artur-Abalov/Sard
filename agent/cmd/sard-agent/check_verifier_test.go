// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// V5, owner's decision 14: a verifier is given credentials that only read;
// repo check (and so sard-agent check) says when they can write.

func verifierCheckHost(t *testing.T) *setupHost {
	t.Helper()
	h := checkHost(t)
	h.deps.hostRole = verifierRole
	return h
}

func TestAVerifierWhoseStorageOnlyReadsPassesTheReadOnlyCheck(t *testing.T) {
	h := verifierCheckHost(t)
	readOnlyStorage(h.base())

	code, stdout, _ := h.repoCheck("base")

	assertCode(t, code, exitOK)
	lines, _ := parseOut(t, stdout)
	l := line(t, lines, hostcheck.CheckRepositoryReadOnly, "base")
	if l.status != "OK" || l.message != "the credentials only read: the storage refuses a lock" {
		t.Fatalf("%+v", l)
	}
	if last := lines[len(lines)-1]; last.check != hostcheck.CheckRepositoryReadOnly {
		t.Errorf("lines %v", lines)
	}
}

func TestAVerifierWhoseStorageCanWriteIsWarned(t *testing.T) {
	h := verifierCheckHost(t)

	_, stdout, _ := h.repoCheck("base")

	lines, _ := parseOut(t, stdout)
	l := line(t, lines, hostcheck.CheckRepositoryReadOnly, "base")
	if l.status != "WARN" || !strings.Contains(l.message, "can write") || !strings.Contains(l.hint, "only read") {
		t.Fatalf("%+v", l)
	}
}

func TestASourceHasNoReadOnlyCheck(t *testing.T) {
	h := checkHost(t)

	_, stdout, _ := h.repoCheck("base")

	lines, _ := parseOut(t, stdout)
	for _, l := range lines {
		if l.check == hostcheck.CheckRepositoryReadOnly {
			t.Fatalf("%+v", l)
		}
	}
}

// A verifier reads the data without a lock, as its verifications do.
func TestTheIntegrityCheckOfAVerifierTakesNoLock(t *testing.T) {
	h := verifierCheckHost(t)
	readOnlyStorage(h.base())

	code, stdout, _ := h.repoCheck("base", "--read-data", "5")

	calls := h.restic.callsTo(h.path("base"), "check")
	if len(calls) != 1 || !slices.Contains(calls[0].args, "--read-data-subset=5%") || !slices.Contains(calls[0].args, "--no-lock") {
		t.Fatalf("check calls %+v", calls)
	}
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, hostcheck.CheckRepositoryIntegrity, "base"); l.status != "OK" {
		t.Fatalf("%+v (code %d)", l, code)
	}
}

// A lock the storage did not answer about (no network) says nothing of the
// credentials: the check cannot tell, and says so.
func TestAVerifierWhoseStorageDoesNotAnswerTheLockCannotTell(t *testing.T) {
	h := verifierCheckHost(t)
	h.base().script = func(sub string, args []string) (string, int, bool) {
		return "Fatal: unable to open repository: dial tcp 10.0.0.9:443: connect: connection refused", 1,
			sub == "snapshots" && !slices.Contains(args, "--no-lock")
	}

	_, stdout, _ := h.repoCheck("base")

	lines, _ := parseOut(t, stdout)
	l := line(t, lines, hostcheck.CheckRepositoryReadOnly, "base")
	if l.status != "WARN" || !strings.Contains(l.message, "cannot tell") || !strings.Contains(l.message, "connection refused") {
		t.Fatalf("%+v", l)
	}
}
