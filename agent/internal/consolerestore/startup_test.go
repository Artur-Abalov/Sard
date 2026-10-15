// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
)

// stuck is the evidence of what the start could not settle.
func stuck(all []consolerestore.Evidence) []consolerestore.Evidence {
	var out []consolerestore.Evidence
	for _, e := range all {
		if e.Verdict == consolerestore.CleanupFailed {
			out = append(out, e)
		}
	}
	return out
}

func leaveStaging(t *testing.T, h *host, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := writeFile(filepath.Join(h.dir, ".sard-staging", n, "srv/a.txt"), "A", 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Scenario: При старте агент удаляет остатки временных каталогов и пишет об этом.
func TestAtStartTheAgentRemovesTheStagingDirectoriesAndLogsEachOne(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1", "c2")
	if err := os.WriteFile(filepath.Join(h.dir, ".sard-staging", "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if left := stuck(h.engine.CleanStaging()); len(left) != 0 {
		t.Fatalf("left = %+v", left)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
	log := h.log.String()
	for _, name := range []string{"c1", "c2", "stray"} {
		if n := strings.Count(log, filepath.Join(h.dir, ".sard-staging", name)); n != 1 {
			t.Errorf("%s is logged %d times:\n%s", name, n, log)
		}
	}
}

func TestWithoutARestoreDirectoryThereIsNothingToClean(t *testing.T) {
	h := newHost(t)
	if left := h.engine.CleanStaging(); len(left) != 0 {
		t.Fatalf("evidence = %+v", left)
	}
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory was made: %v", err)
	}
}

// Scenario: Неудачная уборка при старте называется в результате прерванного шага.
func TestAStagingDirectoryThatCannotBeRemovedAtStartIsLeftAndLoggedAsAnError(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1", "c2")
	c1 := filepath.Join(h.dir, ".sard-staging", "c1")
	h.faults.removeDirErr = map[string]error{c1: syscall.EIO}
	left := stuck(h.engine.CleanStaging())
	if len(left) != 1 || left[0].Name != "c1" || left[0].Path != c1 || left[0].Err == nil {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".sard-staging", "c2")); !os.IsNotExist(err) {
		t.Errorf("c2 was not removed: %v", err)
	}
	mentions(t, h.log.String(), "level=ERROR", c1)
}

func TestACleanupNeverFollowsALinkOfTheStagingDirectory(t *testing.T) {
	h := newHost(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := writeFile(filepath.Join(outside, "keep"), "K", 0o600); err != nil {
		t.Fatal(err)
	}
	mustMkdirAll(t, h.dir)
	if err := os.Symlink(outside, filepath.Join(h.dir, ".sard-staging")); err != nil {
		t.Fatal(err)
	}
	left := stuck(h.engine.CleanStaging())
	if len(left) != 1 || left[0].Path != filepath.Join(h.dir, ".sard-staging") {
		t.Fatalf("left = %+v", left)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Errorf("the target of the link was cleaned: %v", err)
	}
}

func command(h *host, id string) string { return filepath.Join(h.dir, ".sard-staging", id) }

// killedAfterTheMove is a host where a restore finished its move and the agent
// was killed before the executor journaled the result.
func killedAfterTheMove(t *testing.T) *host {
	t.Helper()
	h := newHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	h.rebuild() // the new run of the agent
	return h
}

// Scenario: Убитое после переноса восстановление сообщает об успехе с итогом из метки.
func TestAtStartARestoreKilledAfterItsMoveIsEvidenceOfSuccessAndTheDestinationIsUntouched(t *testing.T) {
	h := killedAfterTheMove(t)
	got := h.engine.CleanStaging()
	want := []consolerestore.Evidence{{Name: "c1", Path: command(h, "c1"), Verdict: consolerestore.Completed, Destination: h.dest(), Files: 2, Bytes: 4}}
	if !slices.Equal(got, want) {
		t.Fatalf("evidence = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(h.dest(), "srv/data/a.txt")); err != nil {
		t.Errorf("the destination was touched: %v", err)
	}
	if _, err := os.Stat(command(h, "c1")); err != nil {
		t.Errorf("the directory with the marker goes only once the result is journaled: %v", err)
	}
}

// Scenario: Каталог с меткой удаляется только после записи результата.
func TestSettleRemovesTheDirectoryOfACommandAndLogsIt(t *testing.T) {
	h := killedAfterTheMove(t)
	h.engine.CleanStaging()
	h.engine.Settle("c1")
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
	mentions(t, h.log.String(), command(h, "c1"))
	if _, err := os.Stat(filepath.Join(h.dest(), "srv/data/a.txt")); err != nil {
		t.Errorf("the destination was touched: %v", err)
	}
}

func TestSettleNeverLeavesTheStagingDirectory(t *testing.T) {
	h := newHost(t)
	keep := filepath.Join(h.dir, "keep")
	if err := writeFile(filepath.Join(keep, "f"), "K", 0o600); err != nil {
		t.Fatal(err)
	}
	leaveStaging(t, h, "c1")
	for _, name := range []string{"../keep", "", ".", ".."} {
		h.engine.Settle(name)
	}
	if _, err := os.Stat(filepath.Join(keep, "f")); err != nil {
		t.Errorf("Settle left .sard-staging: %v", err)
	}
	if _, err := os.Stat(command(h, "c1")); err != nil {
		t.Errorf("c1: %v", err)
	}
}

// Scenario: Убитое во время restic восстановление сообщает о провале и удаляет root.
func TestAtStartARestoreKilledDuringResticIsEvidenceOfNothingRestoredAndRootIsRemoved(t *testing.T) {
	h := newHost(t)
	if err := writeFile(filepath.Join(command(h, "c1"), "root/srv/a.txt"), "A", 0o600); err != nil {
		t.Fatal(err)
	}
	got := h.engine.CleanStaging()
	want := []consolerestore.Evidence{{Name: "c1", Path: command(h, "c1"), Verdict: consolerestore.NothingRestored}}
	if !slices.Equal(got, want) {
		t.Fatalf("evidence = %+v, want %+v", got, want)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
}

// Scenario: Убитое после метки, но до переноса восстановление считается непрошедшим.
func TestAtStartARestoreKilledAfterTheMarkerButBeforeTheMoveIsEvidenceOfNothingRestored(t *testing.T) {
	h := killedAfterTheMove(t)
	// the destination is gone and root is back: the rename did not happen
	if err := os.Rename(h.dest(), filepath.Join(command(h, "c1"), "root")); err != nil {
		t.Fatal(err)
	}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.NothingRestored {
		t.Fatalf("evidence = %+v", got)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
	if _, err := os.Stat(h.dest()); !os.IsNotExist(err) {
		t.Errorf("the destination: %v", err)
	}
}

// Scenario: Принятое, но не начатое восстановление; a directory made and nothing in it.
func TestAtStartAnEmptyDirectoryOfACommandIsEvidenceOfNothingRestored(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, command(h, "c1"))
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.NothingRestored || got[0].Name != "c1" {
		t.Fatalf("evidence = %+v", got)
	}
	if _, err := os.Stat(command(h, "c1")); !os.IsNotExist(err) {
		t.Errorf("c1: %v", err)
	}
}

// Scenario: Неудачная уборка при старте называется в результате прерванного шага (root).
func TestAtStartARootThatCannotBeRemovedIsCleanupFailedNamingTheDirectory(t *testing.T) {
	h := newHost(t)
	leaveStaging(t, h, "c1")
	mustMkdirAll(t, filepath.Join(command(h, "c1"), "root"))
	h.faults.removeDirErr = map[string]error{filepath.Join(command(h, "c1"), "root"): syscall.EIO}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.CleanupFailed || got[0].Path != command(h, "c1") || got[0].Err == nil {
		t.Fatalf("evidence = %+v", got)
	}
	mentions(t, h.log.String(), "level=ERROR", command(h, "c1"))
}

func TestAtStartAMarkerThatCannotBeReadIsCleanupFailedAndTheDirectoryStays(t *testing.T) {
	h := newHost(t)
	if err := writeFile(filepath.Join(command(h, "c1"), "marker"), "{not json", 0o600); err != nil {
		t.Fatal(err)
	}
	got := h.engine.CleanStaging()
	if len(got) != 1 || got[0].Verdict != consolerestore.CleanupFailed || got[0].Err == nil {
		t.Fatalf("evidence = %+v", got)
	}
	if _, err := os.Stat(command(h, "c1")); err != nil {
		t.Errorf("c1: %v", err)
	}
	mentions(t, h.log.String(), "level=ERROR", command(h, "c1"))
}

func TestSettleOfACommandThatCannotBeRemovedLogsAnError(t *testing.T) {
	h := killedAfterTheMove(t)
	h.faults.removeDirErr = map[string]error{command(h, "c1"): syscall.EIO}
	h.engine.Settle("c1")
	mentions(t, h.log.String(), "level=ERROR", command(h, "c1"))
}

func TestSettleWithoutARestoreDirectoryDoesNothing(t *testing.T) {
	h := newHost(t)
	h.engine.Settle("c1")
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory was made: %v", err)
	}
}
