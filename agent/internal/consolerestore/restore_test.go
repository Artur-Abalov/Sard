// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Scenario: Файлы снимка появляются в назначении целиком под исходными путями.
func TestTheSnapshotAppearsInTheDestinationUnderItsOriginalPaths(t *testing.T) {
	h := newHost(t)
	out, f := h.run(h.request())
	if f != nil {
		t.Fatal(f)
	}
	for path, want := range map[string]string{"srv/data/a.txt": "AAA", "srv/data/sub/b.txt": "B"} {
		if got, err := os.ReadFile(filepath.Join(h.dest(), path)); err != nil || string(got) != want {
			t.Errorf("%s: %q, %v", path, got, err)
		}
	}
	if out != (consolerestore.Output{Target: h.dest(), Files: 2, Bytes: 4}) {
		t.Errorf("output = %+v", out)
	}
	if info, err := os.Stat(filepath.Join(h.dest(), "srv/data/a.txt")); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("a.txt: %v, %v", info, err)
	}
}

// Scenario: restic пишет во временный каталог, а назначение появляется одним переносом.
func TestResticWritesIntoTheStagingDirectoryAndTheDestinationAppearsWithOneMove(t *testing.T) {
	h := newHost(t)
	staging := filepath.Join(h.dir, ".sard-staging", "c1")
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		if _, err := os.Stat(filepath.Join(h.dir, "s1")); req.Target != staging || !os.IsNotExist(err) {
			t.Errorf("restic target = %q, want %q; the destination while restic works: %v", req.Target, staging, err)
		}
		return writesF(ctx, req)
	}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if len(h.repo.requests) != 1 || h.repo.requests[0].SnapshotID != snapshotID || h.repo.requests[0].Staging != nil {
		t.Errorf("requests = %+v", h.repo.requests)
	}
	if got := tree(t, filepath.Join(h.dir, ".sard-staging")); !slices.Equal(got, []string{"."}) {
		t.Errorf("staging holds %v", got)
	}
}

// Scenario: Часть путей восстанавливает только выбранное поддерево.
func TestPathsLimitTheRestoreToTheChosenSubtrees(t *testing.T) {
	h := newHost(t)
	h.repo.paths["/srv/data/sub"] = true
	req := h.request()
	req.Plan.Paths = []string{"/srv/data/sub"}
	if _, f := h.run(req); f != nil {
		t.Fatal(f)
	}
	if got := h.repo.requests[0].Include; !slices.Equal(got, []string{"/srv/data/sub"}) {
		t.Errorf("include = %q", got)
	}
}

// Scenario: Ход восстановления виден в фазе RESTORING.
func TestTheProgressOfResticIsReportedInThePhaseRestoring(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		req.Progress(restic.Progress{FilesDone: 1, TotalFiles: 2, BytesDone: 100, TotalBytes: 200})
		return writesF(ctx, req)
	}
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	got := h.rep.events
	if len(got) < 3 || got[0].phase != agentv1.StepPhase_STEP_PHASE_PREPARING {
		t.Fatalf("events = %+v", got)
	}
	want := event{phase: agentv1.StepPhase_STEP_PHASE_RESTORING, bytes: 100, bytesTotal: 200, files: 1, filesTotal: 2}
	if !slices.Contains(got, want) {
		t.Errorf("events = %+v, want one of %+v", got, want)
	}
}

// Scenario: Владельцы из снимка не переносятся, итог говорит об этом.
func TestTheLogSaysThatOwnersAreNotRestored(t *testing.T) {
	h := newHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	found := false
	for _, l := range h.rep.logs() {
		found = found || (l.level == agentv1.LogLevel_LOG_LEVEL_INFO && strings.Contains(l.text, "owners and groups") && strings.Contains(l.text, "not restored"))
	}
	if !found {
		t.Errorf("logs = %+v", h.rep.logs())
	}
}

// Scenario: Каталог восстановления создаётся при первом восстановлении с правами 0700.
func TestTheRestoreDirectoryIsCreatedWithModeZeroSevenHundred(t *testing.T) {
	h := newHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	for _, p := range []string{h.dir, filepath.Join(h.dir, ".sard-staging")} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o700 || info.Sys() == nil {
			t.Errorf("%s: %v, %v", p, info, err)
		}
	}
}

// Scenario: Пустой снимок восстанавливается в пустой каталог.
func TestAnEmptySnapshotIsRestoredIntoAnEmptyDirectory(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, nil
	}
	out, f := h.run(h.request())
	if f != nil || out.Files != 0 || out.Bytes != 0 {
		t.Fatalf("output %+v, failure %v", out, f)
	}
	if got := tree(t, h.dest()); !slices.Equal(got, []string{"."}) {
		t.Errorf("destination holds %v", got)
	}
}

// ВВ12. Scenario: Пустое существующее назначение заменяется восстановленным каталогом.
func TestAnEmptyExistingDestinationIsReplacedByTheRestoredDirectory(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dest())
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	if _, err := os.Stat(filepath.Join(h.dest(), "srv/data/a.txt")); err != nil {
		t.Error(err)
	}
}

// Scenario: Итог называет путь, число файлов и объём. restic counts the
// directories of the snapshot among the files it restores; the output counts
// the files.
func TestTheOutputCountsTheRegularFilesAndTheirBytesNotTheDirectories(t *testing.T) {
	h := newHost(t)
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		if _, err := writesF(ctx, req); err != nil {
			return restic.RestoreSummary{}, err
		}
		if err := os.Symlink("/etc/passwd", filepath.Join(req.Target, "srv", "link")); err != nil {
			return restic.RestoreSummary{}, err
		}
		return restic.RestoreSummary{Files: 6, Bytes: 4}, nil
	}
	out, f := h.run(h.request())
	if f != nil || out.Files != 2 || out.Bytes != 4 {
		t.Fatalf("output %+v, failure %v", out, f)
	}
}
