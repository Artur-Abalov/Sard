// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// Scenarios of A8d-1 (docs/specs/agent/host-check.feature): repo unlock.

func (h *setupHost) unlock(extra ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "unlock", "base", "--config", "C"}, extra...)...)
}

func (h *setupHost) unlocks() []fakeCall { return h.restic.callsTo(h.path("base"), "unlock") }

// Rule "repo unlock снимает только свои зависшие блокировки и только по явному флагу".

func TestAnOwnStaleLockIsRemovedByRestic(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	code, stdout, _ := h.unlock("--remove-stale")
	assertCode(t, code, exitOK)
	calls := h.unlocks()
	if len(calls) != 1 || !slices.Equal(calls[0].args, []string{"unlock"}) {
		t.Fatalf("unlock calls %+v", calls)
	}
	if calls[0].runAs == nil || calls[0].runAs.UID != serviceUID {
		t.Errorf("unlock ran as %+v", calls[0].runAs)
	}
	if !strings.Contains(stdout, "removed 1 stale lock") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestTheRemovalIsPrecededByAWarningAndFollowedByAnAuditLine(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	_, _, stderr := h.unlock("--remove-stale")
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "damages") {
		t.Errorf("stderr %q", stderr)
	}
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository base stale locks removed: 1 by alice") {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestWithoutRemoveStaleNothingIsRemoved(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	before := h.hostTree()
	code, _, stderr := h.unlock()
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--remove-stale") || len(h.restic.subs()) != 0 {
		t.Errorf("stderr %q calls %v", stderr, h.restic.subs())
	}
	h.assertHostUnchanged(before)
}

func TestAForeignLockThatResticWouldRemoveCancelsTheRemoval(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().addLock(h.clock.now, "db2", 48*time.Hour, 77)
	code, _, stderr := h.unlock("--remove-stale")
	assertRefusal(t, code, stderr, exitUsage, "STALE_LOCKS_FOREIGN")
	if !strings.Contains(stderr, "db2") || !strings.Contains(stderr, "48h") || len(h.unlocks()) != 0 || len(h.log.lines) != 0 {
		t.Errorf("stderr %q, unlocks %d, audit %q", stderr, len(h.unlocks()), h.log.lines)
	}
}

func TestAnOwnLockYoungerThanTheThresholdThatResticWouldRemoveCancelsTheRemoval(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().addLock(h.clock.now, "db1", 2*time.Hour, 4243)
	code, _, stderr := h.unlock("--remove-stale")
	assertRefusal(t, code, stderr, exitUsage, "STALE_LOCKS_FOREIGN")
	if !strings.Contains(stderr, "2h") || len(h.unlocks()) != 0 {
		t.Errorf("stderr %q, unlocks %d", stderr, len(h.unlocks()))
	}
}

func TestAFreshForeignLockDoesNotGetInTheWay(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().addLock(h.clock.now, "db2", 5*time.Minute, 77)
	code, _, _ := h.unlock("--remove-stale")
	assertCode(t, code, exitOK)
	if len(h.unlocks()) != 1 {
		t.Errorf("unlock ran %d times", len(h.unlocks()))
	}
}

func TestWithNothingToRemoveNothingIsRun(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.alive[4242] = true
	code, stdout, _ := h.unlock("--remove-stale")
	assertCode(t, code, exitOK)
	if stdout != "no stale locks of this host in repository base\n" || len(h.unlocks()) != 0 || len(h.log.lines) != 0 {
		t.Errorf("stdout %q, unlocks %d, audit %q", stdout, len(h.unlocks()), h.log.lines)
	}
}

func TestTheListIsReadAgainRightBeforeTheRemoval(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().checkState.onList = func(n int) {
		if n == 2 {
			h.base().addLock(h.clock.now, "db2", 48*time.Hour, 77)
		}
	}
	code, _, stderr := h.unlock("--remove-stale")
	assertRefusal(t, code, stderr, exitUsage, "STALE_LOCKS_FOREIGN")
	if !strings.Contains(stderr, "db2") || len(h.unlocks()) != 0 {
		t.Errorf("stderr %q, unlocks %d", stderr, len(h.unlocks()))
	}
}

func TestALockThatWentAwayBetweenTheTwoReadsMeansNothingToRemove(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().checkState.onList = func(n int) {
		if n == 2 {
			h.base().checkState.locks = nil
		}
	}
	code, stdout, _ := h.unlock("--remove-stale")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "no stale locks of this host") || len(h.unlocks()) != 0 {
		t.Errorf("stdout %q, unlocks %d", stdout, len(h.unlocks()))
	}
}

func TestOnlyRootMayRemoveLocks(t *testing.T) {
	h := checkHost(t)
	for _, as := range []func(args ...string) (int, string, string){h.asService, h.asOther} {
		code, _, stderr := as("repo", "unlock", "base", "--remove-stale", "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		if !strings.Contains(stderr, "sudo sard-agent repo unlock base --remove-stale") || len(h.restic.subs()) != 0 {
			t.Errorf("stderr %q calls %v", stderr, h.restic.subs())
		}
	}
}

func TestAnUnreachableStorageIsATemporaryFailure(t *testing.T) {
	h := checkHost(t)
	h.base().script = printsOn("list", "Fatal: dial tcp 192.0.2.1:443: connect: connection refused")
	code, _, stderr := h.unlock("--remove-stale")
	assertRefusal(t, code, stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if len(h.unlocks()) != 0 {
		t.Error("unlock ran")
	}
}

func TestARefusalOfResticUnlockIsToldAndNothingIsRecorded(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.base().checkState.unlockCode = 1
	h.base().checkState.unlockFatal = "Fatal: unable to remove lock: Access Denied."
	code, _, stderr := h.unlock("--remove-stale")
	if code == exitOK || !strings.Contains(stderr, "unlock") || len(h.log.lines) != 0 {
		t.Errorf("code %d, stderr %q, audit %q", code, stderr, h.log.lines)
	}
}

func TestAnUnknownRepositoryIsNamedBeforeAnyAccess(t *testing.T) {
	h := checkHost(t)
	code, _, stderr := h.sudo("repo", "unlock", "nope", "--remove-stale", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
	if h.restic.backendCalls() != 0 {
		t.Errorf("restic %v", h.restic.subs())
	}
}

func TestWithoutTheNameOfThisHostNothingIsRemoved(t *testing.T) {
	h := checkHost(t)
	h.base().addLock(h.clock.now, "db1", 30*time.Hour, 4242)
	h.deps.hostname = func() (string, error) { return "", errors.New("no name") }
	code, _, stderr := h.unlock("--remove-stale")
	assertCode(t, code, exitAgentError)
	if !strings.Contains(stderr, "no name") || len(h.unlocks()) != 0 {
		t.Errorf("stderr %q, unlocks %d", stderr, len(h.unlocks()))
	}
}
