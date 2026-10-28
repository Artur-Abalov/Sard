// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "repo snapshots говорит, подтверждена ли полнота снимка" (PL7-2d,
// Р89-Р91 of docs/specs/agent/repo-restore.feature).

const idFP = "eeee1111" + "00000000000000000000000000000000000000000000000000000000"

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fingerprintOf is the snapshot of the fingerprint of the main snapshot main.
func fingerprintOf(id, main string) restic.Snapshot {
	return restic.Snapshot{ID: id, Time: time.Date(2026, 10, 12, 3, 0, 5, 0, time.UTC), Hostname: "db1", Paths: []string{"/sard-fingerprint.json"},
		Tags: []string{"files.main_snapshot=" + main, "files.part=fingerprint", "sard.fp=" + hex64, "sard.run=r3", "sard.source=s1", "sard.step=st3"}}
}

// withFingerprint is the "набор с отпечатком": the set and FP.
func withFingerprint(t *testing.T) *setupHost {
	t.Helper()
	h := snapshotsHost(t)
	h.base().snapshots = append(h.base().snapshots, fingerprintOf(idFP, idF2))
	return h
}

// completeOf is the COMPLETE column by short id.
func completeOf(t *testing.T, stdout string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, r := range rowsOf(t, stdout) {
		got[r[0]] = r[5]
	}
	return got
}

func TestTheCompletenessOfASnapshotIsConfirmedByTheSnapshotOfItsFingerprint(t *testing.T) {
	h := withFingerprint(t)
	code, stdout, _ := h.snapshots()
	assertCode(t, code, exitOK)
	want := map[string]string{"aaaa2222": "confirmed", "aaaa1111": "unconfirmed", "bbbb1111": "-", "cccc1111": "-", "eeee1111": "-", "dddd1111": "-"}
	if got := completeOf(t, stdout); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWhichTagsMakeTheCompletenessApplicable(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want string
	}{
		{[]string{"sard.run=r1", "sard.source=s1", "sard.step=st1"}, "unconfirmed"},
		{[]string{"sard.run=r1"}, "unconfirmed"},
		{[]string{"sard.run=", "sard.source=s1"}, "-"},
		{[]string{"sard.source=s1"}, "-"},
		{[]string{"sard.run=r1", "acme.part=main"}, "-"},
		{[]string{"sard.run=r1", "sard.fp=" + hex64}, "unconfirmed"},
		{nil, "-"},
	} {
		h := newSetupHost(t)
		h.base().snapshots = []restic.Snapshot{{ID: idF, Time: time.Now(), Hostname: "h", Paths: []string{"/srv/data"}, Tags: c.tags}}
		_, stdout, _ := h.snapshots()
		if got := completeOf(t, stdout)["aaaa1111"]; got != c.want {
			t.Errorf("tags %v: %q, want %q", c.tags, got, c.want)
		}
	}
}

func TestOnlyAFullIDWithBothTagsLinksTheFingerprint(t *testing.T) {
	for name, tags := range map[string][]string{
		"short id":         {"files.main_snapshot=aaaa2222", "files.part=fingerprint"},
		"no part":          {"files.main_snapshot=" + idF2},
		"another part":     {"files.main_snapshot=" + idF2, "files.part=other"},
		"another plugin":   {"acme.main_snapshot=" + idF2, "acme.part=fingerprint"},
		"no main_snapshot": {"files.part=fingerprint"},
	} {
		h := snapshotsHost(t)
		h.base().snapshots = append(h.base().snapshots, restic.Snapshot{ID: idFP, Time: time.Now(), Hostname: "db1", Paths: []string{"/x"}, Tags: tags})
		_, stdout, _ := h.snapshots()
		if got := completeOf(t, stdout)["aaaa2222"]; got != "unconfirmed" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestTheSourceFilterStillSeesTheSnapshotOfTheFingerprint(t *testing.T) {
	h := withFingerprint(t)
	h.base().snapshots = slices.DeleteFunc(h.base().snapshots, func(s restic.Snapshot) bool { return slices.Contains(s.Tags, "sard.source=s2") || s.ID == idM })
	_, stdout, _ := h.snapshots("--source", "s1")
	got := completeOf(t, stdout)
	if got["aaaa2222"] != "confirmed" || got["aaaa1111"] != "unconfirmed" {
		t.Fatalf("got %v", got)
	}
}

func TestMarkingTheCompletenessCostsOneCallAndWritesNothing(t *testing.T) {
	h := withFingerprint(t)
	before := h.hostTree()
	h.snapshots()
	if n := h.restic.backendCalls(); n != 1 {
		t.Fatalf("calls %v", h.restic.subs())
	}
	for _, sub := range []string{"tag", "backup", "forget", "rewrite"} {
		if len(h.restic.callsTo(h.path("base"), sub)) != 0 {
			t.Errorf("restic %s was called", sub)
		}
	}
	h.assertHostUnchanged(before)
}

func TestUnconfirmedSnapshotsAreExplainedByOneLineOnStderr(t *testing.T) {
	h := snapshotsHost(t)
	_, stdout, stderr := h.snapshots()
	for _, want := range []string{"completeness of 2 snapshot(s) is not confirmed", "sard-agent repo snapshots --help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	}
	if strings.Count(stderr, "\n") != 1 || strings.Contains(stdout, "completeness") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestWithoutUnconfirmedSnapshotsStderrIsEmpty(t *testing.T) {
	h := withFingerprint(t)
	h.base().snapshots = slices.DeleteFunc(h.base().snapshots, func(s restic.Snapshot) bool { return s.ID == idF })
	code, _, stderr := h.snapshots()
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
}

func TestThousandsOfFingerprintsAreMarkedInOneCall(t *testing.T) {
	h := newSetupHost(t)
	for i := range 5000 {
		id := fmt.Sprintf("%08x", i) + strings.Repeat("1", 56)
		h.base().snapshots = append(h.base().snapshots,
			restic.Snapshot{ID: id, Time: time.Unix(int64(i), 0), Hostname: "h", Paths: []string{"/x"}, Tags: []string{"sard.run=r"}},
			fingerprintOf(fmt.Sprintf("%08x", i)+strings.Repeat("2", 56), id))
	}
	_, stdout, stderr := h.snapshots()
	confirmed := 0
	for _, r := range rowsOf(t, stdout) {
		if r[5] == "confirmed" {
			confirmed++
		}
	}
	if confirmed != 5000 || stderr != "" || h.restic.backendCalls() != 1 {
		t.Fatalf("confirmed %d, stderr %q, calls %d", confirmed, stderr, h.restic.backendCalls())
	}
}

func TestInJSONTheCompletenessIsAStringOrNull(t *testing.T) {
	h := withFingerprint(t)
	code, stdout, stderr := h.snapshots("--json")
	assertCode(t, code, exitOK)
	var got struct {
		Snapshots []struct {
			ShortID      string  `json:"short_id"`
			Completeness *string `json:"completeness"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	shown := map[string]string{}
	for _, s := range got.Snapshots {
		shown[s.ShortID] = "null"
		if s.Completeness != nil {
			shown[s.ShortID] = *s.Completeness
		}
	}
	want := map[string]string{"aaaa2222": "confirmed", "aaaa1111": "unconfirmed", "bbbb1111": "null", "cccc1111": "null", "eeee1111": "null", "dddd1111": "null"}
	if fmt.Sprint(shown) != fmt.Sprint(want) || stderr != "" {
		t.Fatalf("got %v stderr %q", shown, stderr)
	}
}
