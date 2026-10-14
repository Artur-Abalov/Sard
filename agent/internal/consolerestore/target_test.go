// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Scenario: Ссылка в пути назначения отклоняется без перехода по ней.
func TestALinkInTheDestinationPathIsRefusedWithoutFollowingIt(t *testing.T) {
	h := newHost(t)
	etc := filepath.Join(t.TempDir(), "etc")
	mustMkdirAll(t, etc)
	mustMkdirAll(t, h.dir)
	if err := os.Symlink(etc, filepath.Join(h.dir, "s1")); err != nil {
		t.Fatal(err)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_INVALID (check)")
	mentions(t, f.Error(), filepath.Join(h.dir, "s1"))
	if got := tree(t, etc); !slices.Equal(got, []string{"."}) {
		t.Errorf("the target of the link changed: %v", got)
	}
	if len(h.repo.requests) != 0 {
		t.Error("restic ran")
	}
}

func TestAFileInTheDestinationPathIsRefused(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	if err := os.WriteFile(filepath.Join(h.dir, "s1"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_INVALID (check)")
	mentions(t, f.Error(), filepath.Join(h.dir, "s1"), "not a directory")
}

// Scenario: Непустое назначение — отказ без записи.
func TestANonEmptyDestinationIsRefusedWithoutWriting(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dest())
	if err := os.WriteFile(filepath.Join(h.dest(), "keep.txt"), []byte("K"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := tree(t, h.dir)
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_NOT_EMPTY (check)")
	mentions(t, f.Error(), h.dest())
	if after := tree(t, h.dir); !slices.Equal(before, after) {
		t.Errorf("the restore directory changed: %v -> %v", before, after)
	}
	if len(h.repo.requests) != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Точка монтирования внутри каталога восстановления не годится для назначения.
func TestAMountPointInTheRestoreDirectoryIsRefused(t *testing.T) {
	for name, mount := range map[string]string{"the destination": destRel, "its parent": "s1"} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			mustMkdirAll(t, h.dest())
			h.faults.device = map[string]uint64{filepath.Join(h.dir, mount): 999999}
			before := tree(t, h.dir)
			_, f := h.run(h.request())
			hasPrefix(t, f, "TARGET_INVALID (check)")
			mentions(t, f.Error(), "mount point", filepath.Join(h.dir, mount))
			if after := tree(t, h.dir); !slices.Equal(before, after) || len(h.repo.requests) != 0 {
				t.Errorf("something was written: %v -> %v", before, after)
			}
		})
	}
}

// Scenario: Вторая команда в то же назначение получает отказ «занято».
func TestASecondCommandIntoTheSameDestinationIsBusy(t *testing.T) {
	h := newHost(t)
	inRestic, release := make(chan struct{}), make(chan struct{})
	h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
		close(inRestic)
		<-release
		return writesF(ctx, req)
	}
	first := make(chan error, 1)
	go func() {
		_, f := h.run(h.request())
		if f != nil {
			first <- f
			return
		}
		first <- nil
	}()
	<-inRestic
	second := h.request()
	second.CommandID = "c2"
	_, f := h.engine.Restore(context.Background(), &fakeRepo{snapshots: h.repo.snapshots, paths: map[string]bool{}, restore: writesF}, second, &reporter{})
	hasPrefix(t, f, "TARGET_BUSY (check)")
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("the first restore: %v", err)
	}
	// Once the first is done the destination is free again, but not empty.
	_, f = h.run(second)
	hasPrefix(t, f, "TARGET_NOT_EMPTY (check)")
}

// A destination inside another one that is being restored, or around it, is
// as busy as the same one: the rename of the first would meet the second.
func TestADestinationAboveOrBelowARunningOneIsBusy(t *testing.T) {
	for _, pair := range [][2]string{{"a", "a/b"}, {"a/b", "a"}, {"a/b", "a/b/c/d"}} {
		h := newHost(t)
		inRestic, release := make(chan struct{}), make(chan struct{})
		h.repo.restore = func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
			close(inRestic)
			<-release
			return writesF(ctx, req)
		}
		firstReq := h.request()
		firstReq.Plan.Destination = pair[0]
		first := make(chan *consolerestore.Failure, 1)
		go func() {
			_, f := h.engine.Restore(context.Background(), h.repo, firstReq, &reporter{})
			first <- f
		}()
		<-inRestic
		second := h.request()
		second.CommandID, second.Plan.Destination = "c2", pair[1]
		_, f := h.engine.Restore(context.Background(), &fakeRepo{snapshots: h.repo.snapshots, paths: map[string]bool{}, restore: writesF}, second, &reporter{})
		hasPrefix(t, f, "TARGET_BUSY (check)")
		close(release)
		if f := <-first; f != nil {
			t.Fatalf("%v: the first restore: %v", pair, f)
		}
	}
}

// Scenario: Недоступный для записи каталог восстановления называет настройку юнита.
func TestAReadOnlyRestoreDirectoryNamesTheSettingOfTheUnit(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, h.dir)
	h.faults.mkdirErr = map[string]error{filepath.Join(h.dir, ".sard-staging"): syscall.EROFS}
	before := tree(t, h.dir)
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	mentions(t, f.Error(), h.dir, "ReadWritePaths")
	if after := tree(t, h.dir); !slices.Equal(before, after) {
		t.Errorf("traces: %v -> %v", before, after)
	}
}

func TestARestoreDirectoryTheStepMadeIsRemovedWhenTheStepFailsBeforeRestic(t *testing.T) {
	h := newHost(t)
	h.faults.mkdirErr = map[string]error{filepath.Join(h.dir, ".sard-staging"): syscall.EROFS}
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	if _, err := os.Stat(h.dir); !os.IsNotExist(err) {
		t.Errorf("the restore directory made by the step is still there: %v", err)
	}
}

func TestAnExistingTemporaryDirectoryOfTheCommandIsNotReused(t *testing.T) {
	h := newHost(t)
	mustMkdirAll(t, filepath.Join(h.dir, ".sard-staging", "c1"))
	_, f := h.run(h.request())
	hasPrefix(t, f, "TARGET_WRITE (check)")
	mentions(t, f.Error(), "c1")
	if len(h.repo.requests) != 0 {
		t.Error("restic ran")
	}
	if _, err := os.Stat(filepath.Join(h.dir, ".sard-staging", "c1")); err != nil {
		t.Errorf("the existing directory was removed: %v", err)
	}
}
