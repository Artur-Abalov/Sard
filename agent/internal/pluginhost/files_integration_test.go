// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The files plugin against the pinned restic and a local repository (A6b,
// scenarios tagged @restic). Unreadable files are not tested here: the
// development environment and CI run as root, for whom mode 0000 reads
// (the @unit scenarios use restic's own output instead).

func filesSource(t *testing.T) *pluginhost.Source {
	t.Helper()
	s, err := pluginhost.NewSource(filesplugin.Plugin{AgentVersion: "test"}, pluginhost.NewSecrets(nil, os.ReadFile))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// resticOut runs the pinned restic against the repository and returns stdout.
func (r itRepo) resticOut(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(pinnedRestic(t), append([]string{"-r", r.dir, "--json"}, args...)...)
	cmd.Env = []string{"RESTIC_PASSWORD_FILE=" + r.password, "RESTIC_CACHE_DIR=" + filepath.Join(filepath.Dir(r.dir), "cache"), "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("restic %v: %v", args, err)
	}
	return string(out)
}

func (r itRepo) snapshots(t *testing.T) []struct {
	ID    string   `json:"id"`
	Tags  []string `json:"tags"`
	Paths []string `json:"paths"`
} {
	t.Helper()
	var out []struct {
		ID    string   `json:"id"`
		Tags  []string `json:"tags"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal([]byte(r.resticOut(t, "snapshots")), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// entry is one node of `restic ls --json`.
type entry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

func (r itRepo) ls(t *testing.T, snapshot string) map[string]entry {
	t.Helper()
	out := map[string]entry{}
	for line := range strings.Lines(r.resticOut(t, "ls", snapshot)) {
		var e entry
		if json.Unmarshal([]byte(line), &e) == nil && e.Path != "" {
			out[e.Path] = e
		}
	}
	return out
}

func (r itRepo) backup(t *testing.T, cfg string, tags ...string) restic.BackupSummary {
	t.Helper()
	sum, err := filesSource(t).Backup(context.Background(), sdk.Config(cfg), r.cli, tags, &reporter{})
	if err != nil || sum.SnapshotID == "" {
		t.Fatalf("Backup(%s) = %+v, %v", cfg, sum, err)
	}
	return sum
}

func cfgPaths(p ...string) string {
	b, _ := json.Marshal(map[string]any{"paths": p})
	return string(b)
}

// tree makes the tree D of the scenarios: a.txt of 1000 bytes and sub/b.txt
// of 24, each with a marker.
func tree(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	const marker = "QA-MARKER-5c1e"
	writeFile(t, filepath.Join(d, "a.txt"), strings.Repeat("x", 1000-len(marker))+marker)
	writeFile(t, filepath.Join(d, "sub", "b.txt"), marker+strings.Repeat(" ", 24-len(marker)))
	return d
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Scenario: Бэкап дерева даёт снимок с его файлами.
func TestIntegrationFilesBackupOfATree(t *testing.T) {
	r, d := newRepo(t), tree(t)
	sum := r.backup(t, cfgPaths(d))
	snaps := r.snapshots(t)
	if len(snaps) != 1 || snaps[0].ID != sum.SnapshotID {
		t.Fatalf("snapshots = %+v, summary %+v", snaps, sum)
	}
	nodes := r.ls(t, sum.SnapshotID)
	for _, p := range []string{d, filepath.Join(d, "a.txt"), filepath.Join(d, "sub", "b.txt")} {
		if _, ok := nodes[p]; !ok {
			t.Errorf("snapshot lacks %s", p)
		}
	}
	if got := r.resticOut(t, "dump", sum.SnapshotID, filepath.Join(d, "sub", "b.txt")); !strings.HasPrefix(got, "QA-MARKER-5c1e") {
		t.Errorf("content = %q", got)
	}
}

func TestIntegrationFilesSummaryOfATree(t *testing.T) {
	r, d := newRepo(t), tree(t)
	id, err := r.cli.ID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum := r.backup(t, cfgPaths(d)); sum.RepositoryID != id || sum.TotalBytes != 1024 || sum.AddedBytes == 0 {
		t.Errorf("summary = %+v, repository %s", sum, id)
	}
}

// Scenario: Повторный бэкап неизменного дерева добавляет меньше данных.
func TestIntegrationFilesSecondBackupAddsLess(t *testing.T) {
	r, d := newRepo(t), tree(t)
	first, second := r.backup(t, cfgPaths(d)), r.backup(t, cfgPaths(d))
	if second.SnapshotID == first.SnapshotID || second.TotalBytes != 1024 || second.AddedBytes >= first.AddedBytes {
		t.Errorf("first %+v, second %+v", first, second)
	}
}

// Scenarios: несколько путей, обычный файл, пустой каталог.
func TestIntegrationFilesSeveralPathsAFileAndAnEmptyDirectory(t *testing.T) {
	r, d1, d2 := newRepo(t), tree(t), tree(t)
	sum := r.backup(t, cfgPaths(d1, d2))
	nodes := r.ls(t, sum.SnapshotID)
	if _, ok := nodes[d1]; !ok {
		t.Errorf("no %s", d1)
	}
	if _, ok := nodes[d2]; !ok {
		t.Errorf("no %s", d2)
	}

	f := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(f, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sum := r.backup(t, cfgPaths(f)); sum.TotalBytes != 10 || r.resticOut(t, "dump", sum.SnapshotID, f) != "0123456789" {
		t.Errorf("file: %+v", sum)
	}

	empty := t.TempDir()
	sum = r.backup(t, cfgPaths(empty))
	if _, ok := r.ls(t, sum.SnapshotID)[empty]; !ok || sum.TotalBytes != 0 {
		t.Errorf("empty directory: %+v", sum)
	}
}

// Scenarios: метки шага, шаг без меток.
func TestIntegrationFilesTags(t *testing.T) {
	r, d := newRepo(t), tree(t)
	tagged := r.backup(t, cfgPaths(d), "run=r1", "source=s1")
	plain := r.backup(t, cfgPaths(d))
	for _, s := range r.snapshots(t) {
		switch s.ID {
		case tagged.SnapshotID:
			if !slices.Equal(s.Tags, []string{"run=r1", "source=s1"}) {
				t.Errorf("tags = %v", s.Tags)
			}
		case plain.SnapshotID:
			if len(s.Tags) != 0 {
				t.Errorf("tags = %v", s.Tags)
			}
		}
	}
}

// Scenarios: два шага одновременно.
func TestIntegrationFilesTwoBackupsAtOnceLeaveNoLocks(t *testing.T) {
	r, d1, d2 := newRepo(t), tree(t), tree(t)
	var wg sync.WaitGroup
	sums := make([]restic.BackupSummary, 2)
	for i, d := range []string{d1, d2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sums[i], _ = filesSource(t).Backup(context.Background(), sdk.Config(cfgPaths(d)), r.cli, nil, &reporter{})
		}()
	}
	wg.Wait()
	if sums[0].SnapshotID == "" || sums[1].SnapshotID == "" || sums[0].SnapshotID == sums[1].SnapshotID || len(r.snapshots(t)) != 2 {
		t.Fatalf("summaries = %+v", sums)
	}
	if locks, _ := os.ReadDir(filepath.Join(r.dir, "locks")); len(locks) != 0 {
		t.Errorf("%d locks left", len(locks))
	}
}

// Scenarios: исключения по шаблону и каталогом.
func TestIntegrationFilesExclude(t *testing.T) {
	r, d := newRepo(t), tree(t)
	for _, name := range []string{"app.log", "sub/c.log", "cache/x"} {
		writeFile(t, filepath.Join(d, name), "l")
	}
	cfg, _ := json.Marshal(map[string]any{"paths": []string{d}, "exclude": []string{"*.log", filepath.Join(d, "cache")}})
	sum := r.backup(t, string(cfg))
	nodes := r.ls(t, sum.SnapshotID)
	for p := range nodes {
		if strings.HasSuffix(p, ".log") || strings.Contains(p, "cache") {
			t.Errorf("snapshot contains %s", p)
		}
	}
	for _, p := range []string{"a.txt", "sub/b.txt"} {
		if _, ok := nodes[filepath.Join(d, p)]; !ok {
			t.Errorf("snapshot lacks %s", p)
		}
	}
}

// Scenarios: симлинк внутри дерева и висячий симлинк.
func TestIntegrationFilesSymbolicLinksInsideTheTreeAreKeptAsLinks(t *testing.T) {
	r, d := newRepo(t), tree(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "S2")
	symlink(t, outside, filepath.Join(d, "link"))
	symlink(t, "/nonexistent/target", filepath.Join(d, "dangling"))
	sum := r.backup(t, cfgPaths(d))
	nodes := r.ls(t, sum.SnapshotID)
	for _, name := range []string{"link", "dangling"} {
		if e := nodes[filepath.Join(d, name)]; e.Type != "symlink" {
			t.Errorf("%s = %+v", name, e)
		}
	}
	if _, ok := nodes[outside]; ok {
		t.Error("the target of the link is in the snapshot")
	}
	restored := t.TempDir()
	if err := r.cli.Restore(context.Background(), sum.SnapshotID, restored); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(restored, d, "link")); err != nil || target != outside {
		t.Errorf("restored link: %q, %v", target, err)
	}
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
