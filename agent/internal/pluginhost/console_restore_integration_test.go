// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

// The restore of files from the console against the pinned restic and a
// local repository (R2a, scenarios tagged @restic).

type consoleRepo struct {
	itRepo
	handler  func(*agentv1.RunStep, *reporter) (*agentv1.StepResult, error)
	restoreD string
}

func newConsoleRepo(t *testing.T) consoleRepo {
	t.Helper()
	r := newRepo(t)
	reg, err := sdk.NewRegistry(filesplugin.Plugin{AgentVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "restore")
	engine := consolerestore.New(consolerestore.Options{
		Dir: dir, FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
	})
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, os.ReadFile), func(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
		return r.cli.ForStep(commandID, stderr), name == "it"
	}, t.TempDir(), pluginhost.WithConsoleRestore(engine))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := handlers.Handler("files")
	return consoleRepo{itRepo: r, restoreD: dir, handler: func(s *agentv1.RunStep, rep *reporter) (*agentv1.StepResult, error) {
		return h.Run(context.Background(), s, rep)
	}}
}

func (c consoleRepo) step(snapshot, config string) *agentv1.RunStep {
	return &agentv1.RunStep{
		CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_RESTORE, RepositoryName: "it", SnapshotId: snapshot, ConfigJson: config,
		Tags: map[string]string{"sard.source": "s1", "sard.run": "r9", "sard.step": "st9"},
	}
}

func destination(dest string, paths ...string) string {
	cfg := map[string]any{"destination": dest}
	if len(paths) > 0 {
		cfg["paths"] = paths
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

// wholeSnapshot backs up the tree D (a.txt with mode 0640 and a time of 2020) as the source s1 and restores it.
type wholeSnapshot struct {
	c      consoleRepo
	d      string
	stamp  time.Time
	res    *agentv1.StepResult
	target string
}

func restoreWhole(t *testing.T) wholeSnapshot {
	t.Helper()
	c, d := newConsoleRepo(t), tree(t)
	w := wholeSnapshot{c: c, d: d, stamp: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}
	a := filepath.Join(d, "a.txt")
	if err := os.Chmod(a, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(a, w.stamp, w.stamp); err != nil {
		t.Fatal(err)
	}
	sum := c.backup(t, cfgPaths(d), "sard.source=s1", "sard.run=r1")
	res, err := c.handler(c.step(sum.SnapshotID, destination("s1/20261010T030000Z")), &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	w.res, w.target = res, filepath.Join(c.restoreD, "s1/20261010T030000Z")
	return w
}

// Scenario: Весь снимок восстанавливается под исходными путями байт в байт.
// Scenario: Итог называет путь, число файлов и объём.
func TestIntegrationConsoleRestoreOfTheWholeSnapshotUnderTheOriginalPaths(t *testing.T) {
	w := restoreWhole(t)
	want := &agentv1.RestoreOutput{Target: w.target, FilesRestored: 2, BytesRestored: 1024}
	if !proto.Equal(w.res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", w.res.GetRestore(), want)
	}
	for _, rel := range []string{"a.txt", "sub/b.txt"} {
		original, _ := os.ReadFile(filepath.Join(w.d, rel))
		got, err := os.ReadFile(filepath.Join(w.target, w.d, rel))
		if err != nil || string(got) != string(original) {
			t.Errorf("%s: %q, %v", rel, got, err)
		}
	}
	// restic wrote through the held descriptor into root, and root moved; the
	// marker stays until the executor has journaled the result (ВК13).
	command := filepath.Join(w.c.restoreD, ".sard-staging", "c1")
	if _, err := os.Stat(filepath.Join(command, "root")); !os.IsNotExist(err) {
		t.Errorf("root is still in the staging directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(command, "marker")); err != nil {
		t.Errorf("the marker: %v", err)
	}
}

// The permissions and the time of the snapshot are kept; the owner is the user of the agent (ВВ3).
func TestIntegrationConsoleRestoreKeepsPermissionsAndTimeAndOwnsTheFilesByTheAgent(t *testing.T) {
	w := restoreWhole(t)
	info, err := os.Stat(filepath.Join(w.target, w.d, "a.txt"))
	if err != nil || info.Mode().Perm() != 0o640 || !info.ModTime().Equal(w.stamp) {
		t.Fatalf("a.txt: %v, %v", info, err)
	}
	if uid := fileOwner(info); uid != os.Getuid() {
		t.Errorf("owner of a.txt = %d, want the user of the agent %d", uid, os.Getuid())
	}
}

// Scenario: Восстановление не оставляет блокировок в репозитории и не создаёт снимков.
func TestIntegrationConsoleRestoreLeavesNoLocksAndMakesNoSnapshots(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s1")
	before := len(c.snapshots(t))
	if _, err := c.handler(c.step(sum.SnapshotID, destination("x")), &reporter{}); err != nil {
		t.Fatal(err)
	}
	if locks := strings.TrimSpace(c.resticOut(t, "list", "locks")); locks != "" {
		t.Errorf("locks: %q", locks)
	}
	if after := len(c.snapshots(t)); after != before {
		t.Errorf("snapshots: %d -> %d", before, after)
	}
}

// Scenario: Часть путей восстанавливает только выбранное поддерево.
func TestIntegrationConsoleRestoreOfAPathRestoresOnlyTheSubtree(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s1")
	res, err := c.handler(c.step(sum.SnapshotID, destination("part", filepath.Join(d, "sub"))), &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(res.GetRestore().GetTarget(), func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if want := []string{filepath.Join(res.GetRestore().GetTarget(), d, "sub", "b.txt")}; !slices.Equal(files, want) {
		t.Errorf("files = %q, want %q", files, want)
	}
}

// Scenario: Путь, которого нет в снимке, — отказ до записи (restic ls).
func TestIntegrationConsoleRestoreOfAPathThatIsNotInTheSnapshotWritesNothing(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s1")
	_, err := c.handler(c.step(sum.SnapshotID, destination("x", "/srv/nothing")), &reporter{})
	if err == nil || !strings.HasPrefix(err.Error(), "PATH_NOT_IN_SNAPSHOT (check)") || !strings.Contains(err.Error(), "/srv/nothing") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(c.restoreD); !os.IsNotExist(statErr) {
		t.Errorf("the restore directory exists: %v", statErr)
	}
}

// Scenario: Снимок другого источника — отказ без записи.
func TestIntegrationConsoleRestoreOfASnapshotOfAnotherSourceWritesNothing(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s7")
	_, err := c.handler(c.step(sum.SnapshotID, destination("x")), &reporter{})
	if err == nil || !strings.HasPrefix(err.Error(), "SNAPSHOT_UNKNOWN (check)") {
		t.Fatalf("err = %v", err)
	}
}

// ВВ12. Scenario: Пустое существующее назначение заменяется восстановленным каталогом.
func TestIntegrationConsoleRestoreIntoAnEmptyExistingDestination(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s1")
	if err := os.MkdirAll(filepath.Join(c.restoreD, "s1"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := c.handler(c.step(sum.SnapshotID, destination("s1")), &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(res.GetRestore().GetTarget(), d, "a.txt")); err != nil {
		t.Error(err)
	}
}

// Scenario: Пустой снимок восстанавливается в пустой каталог.
func TestIntegrationConsoleRestoreOfAnEmptyDirectoryIsAnEmptyDestination(t *testing.T) {
	c := newConsoleRepo(t)
	empty := t.TempDir()
	sum := c.backup(t, cfgPaths(empty), "sard.source=s1")
	res, err := c.handler(c.step(sum.SnapshotID, destination("empty")), &reporter{})
	if err != nil || res.GetRestore().GetFilesRestored() != 0 || res.GetRestore().GetBytesRestored() != 0 {
		t.Fatalf("Run = %v, %v", res, err)
	}
	if info, err := os.Stat(res.GetRestore().GetTarget()); err != nil || !info.IsDir() {
		t.Errorf("destination: %v, %v", info, err)
	}
}

// Scenario: Восстановление в непустое назначение — отказ без записи.
func TestIntegrationConsoleRestoreIntoANonEmptyDestinationChangesNothing(t *testing.T) {
	c, d := newConsoleRepo(t), tree(t)
	sum := c.backup(t, cfgPaths(d), "sard.source=s1")
	keep := filepath.Join(c.restoreD, "x", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(keep), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("K"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := c.handler(c.step(sum.SnapshotID, destination("x")), &reporter{})
	if err == nil || !strings.HasPrefix(err.Error(), "TARGET_NOT_EMPTY (check)") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(keep); string(got) != "K" {
		t.Errorf("keep.txt: %q", got)
	}
}
