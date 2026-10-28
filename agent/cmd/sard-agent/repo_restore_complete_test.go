// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "repo restore предупреждает о неподтверждённой полноте, но
// восстанавливает" (PL7-2d, Р92 of docs/specs/agent/repo-restore.feature).

const completenessWarning = "warning: completeness of snapshot "

func TestRestoringASnapshotWithUnconfirmedCompletenessWarnsAndRestores(t *testing.T) {
	h := restoreHost(t)
	code, stdout, stderr := h.restore(idF)
	assertCode(t, code, exitOK)
	if !strings.Contains(stderr, completenessWarning+"aaaa1111 is not confirmed: some files of the source may be missing from it") {
		t.Errorf("stderr %q", stderr)
	}
	if !strings.Contains(stdout, "completeness: not confirmed") || !strings.Contains(stdout, "restored") {
		t.Errorf("stdout %q", stdout)
	}
	h.assertRestored(idF)
}

func TestRestoringASnapshotWithConfirmedCompletenessSaysNothingOfIt(t *testing.T) {
	h := restoreHost(t)
	h.base().snapshots = append(h.base().snapshots, fingerprintOf(idFP, idF2))
	code, stdout, stderr := h.restore(idF2)
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "completeness") || strings.Contains(stdout, "completeness") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestRestoringAPostgreSQLSnapshotSaysNothingOfCompleteness(t *testing.T) {
	h := restoreHost(t)
	code, stdout, stderr := h.restore(idP)
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "completeness") || strings.Contains(stdout, "completeness") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestLatestWarnsAboutAnUnconfirmedNewestSnapshot(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore("latest", "--source", "s1")
	assertCode(t, code, exitOK)
	h.assertRestored(idF2)
	if !strings.Contains(stderr, completenessWarning+"aaaa2222 is not confirmed") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestLatestDoesNotSkipAnUnconfirmedSnapshotForAnOlderConfirmedOne(t *testing.T) {
	h := restoreHost(t)
	h.base().snapshots = append(h.base().snapshots, restic.Snapshot{ID: idFP, Time: time.Date(2026, 10, 10, 3, 0, 5, 0, time.UTC), Hostname: "db1",
		Paths: []string{"/sard-fingerprint.json"}, Tags: []string{"files.main_snapshot=" + idF, "files.part=fingerprint", "sard.run=r1", "sard.source=s1", "sard.step=st1"}})
	code, _, stderr := h.restore("latest", "--source", "s1")
	assertCode(t, code, exitOK)
	h.assertRestored(idF2)
	if !strings.Contains(stderr, completenessWarning+"aaaa2222") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestRestoringAPartOfAnUnconfirmedSnapshotWarnsToo(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore(idF, "--path", "/srv/data/a.txt")
	assertCode(t, code, exitOK)
	if !strings.Contains(stderr, completenessWarning+"aaaa1111") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestAFailedRestoreOfAnUnconfirmedSnapshotKeepsItsClassAndWarns(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.fatal = "Fatal: unable to open pack: dial tcp: connection refused"
	code, _, stderr := h.restore(idF)
	assertCode(t, code, exitTemporary)
	if !strings.Contains(stderr, "BACKEND_UNAVAILABLE") || !strings.Contains(stderr, completenessWarning+"aaaa1111") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestARefusalBeforeTheSnapshotIsChosenDoesNotWarnOfCompleteness(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore("eeee9999")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "SNAPSHOT_UNKNOWN") || strings.Contains(stderr, "completeness") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestTheAuditLineOfAnUnconfirmedSnapshotIsTheUsualOne(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	want := "repository base snapshot aaaa1111 restored to " + h.target()
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], want) || strings.Contains(h.log.lines[0], "completeness") {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestRestoreHelpExplainsTheWarningOfUnconfirmedCompleteness(t *testing.T) {
	h := newSetupHost(t)
	_, stdout, _ := h.sudo("repo", "restore", "--help")
	for _, want := range []string{"completeness of the snapshot is not confirmed", "There is no flag", "latest only"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}
