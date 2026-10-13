// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Rule "Сбой восстановления каталога называет причину и что осталось в цели".

const partialText = "partially restored"

func TestWithoutATimeoutTheRestoreHasNoOverallLimit(t *testing.T) {
	h := restoreHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().restoreState.before = func(string) {
		vc.advance(5 * time.Minute)
		time.Sleep(20 * time.Millisecond) // a timer that fired would have ended the context by now
	}
	code, _, stderr := h.restore(idF)
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
}

func TestTheTimeoutEndsAHungRestore(t *testing.T) {
	h := restoreHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.base().restoreState.hang = true
	done := h.runInBackground("repo", "restore", "base", idF, "--target", h.target(), "--config", "C", "--timeout", "2s")
	for range 2000 {
		if len(h.restoreCalls()) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	vc.waitTimers(t, 2)
	vc.advance(2 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "TIMEOUT")
	if !strings.Contains(r.stderr, partialText) || !h.base().terminated {
		t.Errorf("stderr %q, terminated %v", r.stderr, h.base().terminated)
	}
}

func TestAnInterruptStopsResticAndNamesThePartialResult(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.hang = true
	ctx, cancel := context.WithCancel(context.Background())
	done := h.restoreInBackground(ctx)
	h.waitForRestore()
	cancel()
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "INTERRUPTED")
	if !strings.Contains(r.stderr, partialText) || !h.base().terminated {
		t.Errorf("stderr %q, terminated %v", r.stderr, h.base().terminated)
	}
}

func TestFilesThatCouldNotBeRestoredAreAnAgentErrorWithTheNumberAndThePaths(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.items = 12
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitAgentError, "RESTORE_INCOMPLETE")
	for _, want := range []string{"12", "/srv/data/f00", "/srv/data/f09"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	if strings.Contains(stderr, "/srv/data/f10") {
		t.Errorf("more than ten paths: %s", stderr)
	}
	if line := h.lastAudit(); !strings.Contains(line, "repository base snapshot aaaa1111 restore to") || !strings.Contains(line, "failed: RESTORE_INCOMPLETE") {
		t.Errorf("audit %q", line)
	}
}

func TestADiskThatIsFullIsATargetWriteFailure(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.fatal = "Fatal: write " + h.target() + "/srv/data/a.txt: no space left on device"
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	for _, want := range []string{h.target(), partialText} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	if line := h.lastAudit(); !strings.Contains(line, "repository base snapshot aaaa1111 restore to "+h.target()+" failed: TARGET_WRITE") {
		t.Errorf("audit %q", line)
	}
}

func TestAStorageThatIsLostMidwayIsTemporaryAndNamesThePartialResult(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.fatal = "Fatal: unable to open pack: dial tcp: connection refused"
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if !strings.Contains(stderr, partialText) {
		t.Errorf("stderr %q", stderr)
	}
}

func TestACreatedTargetThatStaysEmptyAfterAFailureIsRemoved(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.fatal = "Fatal: unable to open pack: dial tcp: connection refused"
	code, _, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", h.path("a/b"), "--config", "C")
	assertCode(t, code, exitTemporary)
	h.assertAbsent(h.path("a"))
}

func TestAFailureBeforeTheTargetIsMadeDoesNotMakeIt(t *testing.T) {
	h := restoreHost(t)
	h.base().script = func(sub string, _ []string) (string, int, bool) {
		return "Fatal: dial tcp: connection refused", 1, sub == "snapshots"
	}
	code, _, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", h.path("a/b"), "--config", "C")
	assertCode(t, code, exitTemporary)
	h.assertAbsent(h.path("a"))
	if len(h.log.lines) != 0 {
		t.Errorf("audit %v", h.log.lines)
	}
}

func TestPartiallyRestoredFilesStayInTheTarget(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.items = 1
	h.base().restoreState.before = func(target string) {
		ok(t, os.MkdirAll(target+"/srv", 0o755))
		ok(t, os.WriteFile(target+"/srv/partial", []byte("x"), 0o600))
	}
	h.restore(idF)
	if got := h.fileContent(h.target() + "/srv/partial"); got != "x" {
		t.Errorf("partial = %q", got)
	}
}

func TestASecondRestoreIntoTheSameTargetIsBusy(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.hang = true
	ctx, cancel := context.WithCancel(context.Background())
	first := h.restoreInBackground(ctx)
	h.waitForRestore()
	code, _, stderr := h.restore(idF, "--overwrite")
	assertRefusal(t, code, stderr, exitTemporary, "TARGET_BUSY")
	if !strings.Contains(stderr, h.target()) {
		t.Errorf("stderr %q", stderr)
	}
	if n := len(h.restoreCalls()); n != 1 {
		t.Errorf("%d restore calls: the second restore must not call restic", n)
	}
	cancel()
	within(t, first)
}
