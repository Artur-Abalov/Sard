// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// Rules of A8c-2 (docs/specs/agent/repo-restore.feature): repo restore of a
// directory. The "набор снимков" is that of repo_snapshots_test.go.

// restoreHost is a host whose repository base holds the set of snapshots
// with the files of the conventions: F and F2 hold /srv/data/a.txt and
// /srv/data/sub/b.txt, P /app.dump, G /app.globals.sql, M /etc/hosts.
func restoreHost(t *testing.T) *setupHost {
	t.Helper()
	h := snapshotsHost(t)
	two := []snapFile{{"/srv/data/a.txt", "A-DATA"}, {"/srv/data/sub/b.txt", "B-DATA"}}
	// the PATH of restic holds setpriv, as the package util-linux brings it
	h.write(h.path("bin/setpriv"), "#!/bin/sh\n", 0o755)
	h.deps.pathEnv = h.path("bin")
	h.base().restoreState.files = map[string][]snapFile{
		idF: two, idF2: two,
		idP: {{"/app.dump", "DUMP-BYTES"}}, idG: {{"/app.globals.sql", "GLOBALS-BYTES"}},
		idM: {{"/etc/hosts", "HOSTS"}},
	}
	return h
}

// target is T of the conventions: D/restore, which does not exist.
func (h *setupHost) target() string { return h.path("restore") }

// restore is "repo restore base <ref> --target T", as root.
func (h *setupHost) restore(ref string, extra ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "restore", "base", ref, "--target", h.target(), "--config", "C"}, extra...)...)
}

// restoreCalls are the calls of restic restore to the repository base.
func (h *setupHost) restoreCalls() []fakeCall { return h.restic.callsTo(h.path("base"), "restore") }

// assertRestored is "restic получил полный id снимка": exactly one restore, of that snapshot.
func (h *setupHost) assertRestored(id string) {
	h.t.Helper()
	calls := h.restoreCalls()
	if len(calls) != 1 || calls[0].args[len(calls[0].args)-1] != id {
		h.t.Fatalf("restore calls %v, want one of %s", calls, id)
	}
}

func TestASnapshotIsRestoredByItsFullID(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore(idF)
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	h.assertRestored(idF)
	if got := h.fileContent(h.target() + "/srv/data/a.txt"); got != "A-DATA" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestAPrefixOfEightCharactersIsReplacedByTheFullID(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.restore("aaaa1111")
	assertCode(t, code, exitOK)
	h.assertRestored(idF)
}

func TestAnUnusableSnapshotIsAUsageErrorBeforeTheStorageIsTouched(t *testing.T) {
	for _, ref := range []string{"aaaa111", "AAAA1111", "zzzz1111", "", strings.Repeat("a", 65), "-aaaa1111"} {
		h := restoreHost(t)
		before := h.hostTree()
		code, _, stderr := h.restore(ref)
		assertCode(t, code, exitUsage)
		if stderr == "" {
			t.Errorf("%q: no message", ref)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
	}
}

func TestAnAmbiguousPrefixNamesTheCandidates(t *testing.T) {
	h := restoreHost(t)
	h.base().snapshots[0].ID = "aaaa1111e" + strings.Repeat("0", 55)
	h.base().snapshots[3].ID = "aaaa1111f" + strings.Repeat("0", 55)
	before := h.hostTree()
	code, _, stderr := h.restore("aaaa1111")
	assertRefusal(t, code, stderr, exitUsage, "SNAPSHOT_AMBIGUOUS")
	for _, want := range []string{"aaaa1111e", "aaaa1111f", "2026-10-12T03:00:00Z", "2026-10-10T03:00:00Z", "s1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	if len(h.restoreCalls()) != 0 {
		t.Error("restic restore was called")
	}
	h.assertHostUnchanged(before)
}

func TestAnUnknownSnapshotPointsToTheList(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, stderr := h.restore("eeee1111")
	assertRefusal(t, code, stderr, exitUsage, "SNAPSHOT_UNKNOWN")
	if !strings.Contains(stderr, "repo snapshots base") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestLatestIsTheNewestSnapshotOfTheSource(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.restore("latest", "--source", "s1")
	assertCode(t, code, exitOK)
	h.assertRestored(idF2)
	lists := h.restic.callsTo(h.path("base"), "snapshots")
	if len(lists) != 1 || !slices.Contains(lists[0].args, "sard.source=s1") {
		t.Errorf("list calls %v", lists)
	}
}

func TestLatestSkipsTheSnapshotOfTheGlobalsOfPostgreSQL(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.restore("latest", "--source", "s2")
	assertCode(t, code, exitOK)
	h.assertRestored(idP)
}

func TestLatestWithoutASourceIsAUsageErrorNamingTheFlag(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore("latest")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--source") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}

func TestLatestOfASourceWithoutSnapshotsIsUnknownAndNamesTheSource(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, stderr := h.restore("latest", "--source", "s9")
	assertRefusal(t, code, stderr, exitUsage, "SNAPSHOT_UNKNOWN")
	if !strings.Contains(stderr, "s9") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

// lastAudit is the last line of the system log, "" if there is none.
func (h *setupHost) lastAudit() string {
	if len(h.log.lines) == 0 {
		return ""
	}
	return h.log.lines[len(h.log.lines)-1]
}

// restoreInBackground is "repo restore base <F> --target T" as root, started
// in the background; cancelling ctx is the interrupt.
func (h *setupHost) restoreInBackground(ctx context.Context, extra ...string) <-chan cmdResult {
	done := make(chan cmdResult, 1)
	h.deps.euid = 0
	args := h.subst(append([]string{"restore", "base", idF, "--target", h.target(), "--config", "C"}, extra...))
	go func() {
		var out, errOut bytes.Buffer
		code := runRepoWithDeps(ctx, args, &out, &errOut, h.deps)
		done <- cmdResult{code, out.String(), errOut.String()}
	}()
	return done
}

// waitForRestore waits until restic restore has been started.
func (h *setupHost) waitForRestore() {
	h.t.Helper()
	for range 2000 {
		if len(h.restoreCalls()) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatal("restic restore was not called")
}

// Р75: --source narrows the search by id or prefix as well.
func TestSourceWithTheIDOfASnapshotOfThatSourceRestoresIt(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore("aaaa1111", "--source", "s1")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	h.assertRestored(idF)
}

func TestSourceWithTheIDOfASnapshotOfAnotherSourceFindsNoSnapshot(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, stderr := h.restore("bbbb1111", "--source", "s1")
	assertRefusal(t, code, stderr, exitUsage, "SNAPSHOT_UNKNOWN")
	for _, want := range []string{"bbbb1111", "s1"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	h.assertHostUnchanged(before)
}

func TestAnAmbiguousPrefixIsCountedAmongTheSnapshotsOfTheSourceOnly(t *testing.T) {
	h := restoreHost(t)
	h.base().snapshots[3].ID = "aaaa1111e" + strings.Repeat("0", 55) // F, source s1
	h.base().snapshots[4].ID = "aaaa1111f" + strings.Repeat("0", 55) // P, source s2
	code, _, stderr := h.restore("aaaa1111", "--source", "s1")
	if code != exitOK {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if calls := h.restoreCalls(); len(calls) != 1 || !strings.HasPrefix(calls[0].args[len(calls[0].args)-1], "aaaa1111e") {
		t.Errorf("restore calls %v", calls)
	}
}
