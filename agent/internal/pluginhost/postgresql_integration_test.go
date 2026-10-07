// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The postgresql plugin against the pinned restic and a local repository
// (F1, scenarios tagged @restic). pg_dump, psql and pg_dumpall are scripts
// of the test: executables that print the bytes and exit with the code the
// scenario says.

// pgHost is a directory with the fake tools and the config of a step.
type pgHost struct {
	dir     string
	secrets *pluginhost.Secrets
}

// pgTools writes the tools into a new directory. pgDump is the body of the
// fake pg_dump (after the --version check), pgDumpall that of pg_dumpall.
func pgTools(t *testing.T, pgDump, pgDumpall string) pgHost {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		writeFile(t, filepath.Join(dir, name), "#!/bin/sh\n"+body)
		if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("pg_dump", `if [ "$1" = "--version" ]; then echo "pg_dump (PostgreSQL) 18.0"; exit 0; fi`+"\n"+pgDump)
	write("pg_dumpall", `if [ "$1" = "--version" ]; then echo "pg_dumpall (PostgreSQL) 18.0"; exit 0; fi`+"\n"+pgDumpall)
	write("psql", `echo "160004|f|t|backup"`)
	password := filepath.Join(dir, "pg-app.secret")
	writeFile(t, password, "pw\n")
	return pgHost{dir: dir, secrets: pluginhost.NewSecrets(map[string]string{"pg-app": password}, os.ReadFile)}
}

// archiveOf is a script body that prints PGDMP and n-5 more bytes.
func archiveOf(n int) string {
	return fmt.Sprintf("printf PGDMP; head -c %d /dev/zero | tr '\\000' x\n", n-5)
}

func (h pgHost) source(t *testing.T) *pluginhost.Source {
	t.Helper()
	s, err := pluginhost.NewSource(postgresql.Plugin{AgentVersion: "test"}, h.secrets)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// config is the config K (or KG with globals) of the tools of the host.
func (h pgHost) config(globals bool, over map[string]any) sdk.Config {
	cfg := map[string]any{"host": "db", "database": "app", "user": "backup", "password_ref": "pg-app", "tls_mode": "disable",
		"include_globals": globals, "pg_dump_path": filepath.Join(h.dir, "pg_dump")}
	for k, v := range over {
		cfg[k] = v
	}
	b, _ := json.Marshal(cfg)
	return b
}

var stepTags = []string{"sard.run=r1", "sard.source=s1"}

func (r itRepo) count(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(r.dir, dir), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// dumpOf is `restic dump` of one file of a snapshot.
func (r itRepo) dumpOf(t *testing.T, snapshot, file string) string {
	t.Helper()
	cmd := r.resticOut(t, "dump", snapshot, file)
	return cmd
}

// processesOf lists the live processes whose executable lies in dir.
func processesOf(dir string) []string {
	var out []string
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err == nil && bytes.Contains(cmdline, []byte(dir)) {
			stat, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
			if i := bytes.LastIndexByte(stat, ')'); i >= 0 && !bytes.HasPrefix(stat[i+2:], []byte("Z")) {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

// Scenario: Успешный поток даёт снимок с одним файлом дампа.
func TestIntegrationPostgresqlStreamGivesASnapshotWithOneDumpFile(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(1<<20), "")
	sum, err := h.source(t).Backup(context.Background(), h.config(false, nil), r.cli, stepTags, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if snaps := r.snapshots(t); len(snaps) != 1 || snaps[0].ID != sum.SnapshotID {
		t.Fatalf("snapshots = %+v, summary %+v", snaps, sum)
	}
	r.wantOnlyFile(t, sum.SnapshotID, "/app.dump")
	want := "PGDMP" + strings.Repeat("x", 1<<20-5)
	if got := r.dumpOf(t, sum.SnapshotID, "/app.dump"); got != want {
		t.Errorf("the dump differs: %d bytes", len(got))
	}
	id, _ := r.cli.ID(context.Background())
	if sum.RepositoryID != id || sum.TotalBytes != 1048576 {
		t.Errorf("summary = %+v", sum)
	}
}

// wantOnlyFile: the snapshot holds exactly one file, named name.
func (r itRepo) wantOnlyFile(t *testing.T, snapshot, name string) {
	t.Helper()
	nodes := r.ls(t, snapshot)
	if e, ok := nodes[name]; !ok || e.Type != "file" || len(nodes) != 1 {
		t.Errorf("nodes of %s = %+v, want only %s", snapshot, nodes, name)
	}
}

// Scenario: Снимок получает метки шага и метки плагина.
func TestIntegrationPostgresqlSnapshotGetsTheTagsOfTheStepAndThePlugin(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(1000), "")
	sum, err := h.source(t).Backup(context.Background(), h.config(false, nil), r.cli, stepTags, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	got := r.snapshots(t)[0].Tags
	slices.Sort(got)
	want := []string{"postgresql.database=app", "postgresql.format=custom", "postgresql.part=database", "postgresql.pg_dump_version=18.0",
		"postgresql.server_version=16.4", "sard.run=r1", "sard.source=s1"}
	if !slices.Equal(got, want) || r.snapshots(t)[0].ID != sum.SnapshotID {
		t.Errorf("tags = %q\nwant   %q", got, want)
	}
}

// Scenario: Дамп не пишется на диск хоста.
func TestIntegrationPostgresqlDumpIsNotWrittenToTheDisk(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(64<<20), "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	biggest, stop := watchFiles(tmp, h.dir, filepath.Join(filepath.Dir(r.dir), "cache"))
	_, err := h.source(t).Backup(context.Background(), h.config(false, nil), r.cli, stepTags, &reporter{})
	size := stop()
	if err != nil {
		t.Fatal(err)
	}
	if size > 1<<20 || biggest() > 1<<20 {
		t.Errorf("a file of %d bytes appeared in the temporary directories", max(size, biggest()))
	}
}

// watchFiles follows the size of the biggest file below the roots until stop
// is called, which returns it.
func watchFiles(roots ...string) (current func() int64, stop func() int64) {
	var biggest atomic.Int64
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			for _, root := range roots {
				_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						raise(&biggest, d)
					}
					return nil
				})
			}
			select {
			case <-quit:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return biggest.Load, func() int64 { close(quit); <-done; return biggest.Load() }
}

// raise lifts max to the size of the file.
func raise(max *atomic.Int64, d fs.DirEntry) {
	if info, err := d.Info(); err == nil && info.Size() > max.Load() {
		max.Store(info.Size())
	}
}

// Scenario: Сбой pg_dump проваливает шаг и не оставляет снимка.
func TestIntegrationPostgresqlFailureLeavesNoSnapshot(t *testing.T) {
	cases := []struct{ name, body, reason string }{
		{"exit 1 after 10 MiB", archiveOf(10<<20) + "echo 'pg_dump: error: connection lost' >&2; exit 1", "connection lost"},
		{"killed", archiveOf(10<<20) + "kill -9 $$", "signal: killed"},
		{"no output", "exit 0", "empty output"},
		{"plain SQL", "echo '-- plain SQL'", "not a custom-format archive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, h := newRepo(t), pgTools(t, c.body, "")
			sum, err := h.source(t).Backup(context.Background(), h.config(false, nil), r.cli, stepTags, &reporter{})
			if err == nil || !strings.Contains(err.Error(), "pg_dump") || !strings.Contains(err.Error(), c.reason) || sum.SnapshotID != "" {
				t.Fatalf("summary %+v, err = %v", sum, err)
			}
			r.wantNothingLeft(t, h)
		})
	}
}

// wantNothingLeft: no snapshot, no lock, no process of the tools or of restic.
func (r itRepo) wantNothingLeft(t *testing.T, h pgHost) {
	t.Helper()
	if r.count(t, "snapshots") != 0 || r.count(t, "locks") != 0 {
		t.Errorf("snapshots %d, locks %d", r.count(t, "snapshots"), r.count(t, "locks"))
	}
	if p := processesOf(h.dir); len(p) != 0 {
		t.Errorf("tools still running: %v", p)
	}
	if p := resticProcesses(pinnedRestic(t)); len(p) != 0 {
		t.Errorf("restic still running: %v", p)
	}
}

// snapshotInfo is a snapshot as `restic snapshots` lists it.
type snapshotInfo = struct {
	ID    string   `json:"id"`
	Tags  []string `json:"tags"`
	Paths []string `json:"paths"`
}

// Scenario: Бэкап по умолчанию даёт снимок базы и связанный снимок глобальных объектов.
func TestIntegrationPostgresqlDefaultBackupGivesTwoLinkedSnapshots(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(1000), `printf 'CREATE ROLE app_owner;'`)
	sum, err := h.source(t).Backup(context.Background(), h.config(true, nil), r.cli, stepTags, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	snaps := r.snapshots(t)
	i := slices.IndexFunc(snaps, func(s snapshotInfo) bool { return slices.Contains(s.Tags, "postgresql.part=globals") })
	if len(snaps) != 2 || i < 0 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	globals := snaps[i]
	slices.Sort(globals.Tags)
	want := []string{"postgresql.database=app", "postgresql.format=plain", "postgresql.main_snapshot=" + sum.SnapshotID, "postgresql.part=globals",
		"postgresql.pg_dump_version=18.0", "postgresql.role_passwords=false", "postgresql.server_version=16.4", "sard.run=r1", "sard.source=s1"}
	if !slices.Equal(globals.Tags, want) {
		t.Errorf("tags = %q\nwant   %q", globals.Tags, want)
	}
	if got := r.dumpOf(t, globals.ID, "/app.globals.sql"); got != "CREATE ROLE app_owner;" {
		t.Errorf("globals = %q", got)
	}
	r.wantOnlyFile(t, globals.ID, "/app.globals.sql")
	r.wantOnlyFile(t, sum.SnapshotID, "/app.dump")
}

// Scenario: Объёмы результата — суммы по снимку базы и снимку глобальных объектов.
func TestIntegrationPostgresqlVolumesAreSumsOverBothSnapshots(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(5000), `printf 'CREATE ROLE app_owner;'`)
	sum, err := h.source(t).Backup(context.Background(), h.config(true, nil), r.cli, stepTags, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalBytes != 5000+uint64(len("CREATE ROLE app_owner;")) || sum.AddedBytes == 0 {
		t.Errorf("summary = %+v", sum)
	}
}

// Scenario: Сбой дампа базы отбрасывает глобальные объекты.
func TestIntegrationPostgresqlDumpFailureDiscardsTheGlobals(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(1<<20)+"exit 1", `printf 'CREATE ROLE app_owner;'`)
	if _, err := h.source(t).Backup(context.Background(), h.config(true, nil), r.cli, stepTags, &reporter{}); err == nil {
		t.Fatal("no error")
	}
	if r.count(t, "snapshots") != 0 || r.count(t, "locks") != 0 {
		t.Errorf("snapshots %d, locks %d", r.count(t, "snapshots"), r.count(t, "locks"))
	}
}

// cancelOnSecond cancels the step when the second restic backup starts.
type cancelOnSecond struct {
	restic.Repository
	cancel context.CancelFunc
	n      int
}

func (c *cancelOnSecond) Backup(ctx context.Context, req restic.BackupRequest, progress func(restic.Progress)) (restic.BackupSummary, error) {
	c.n++
	if c.n == 2 {
		c.cancel()
	}
	return c.Repository.Backup(ctx, req, progress)
}

// Scenario: Отмена во время сохранения глобальных объектов оставляет только снимок базы.
func TestIntegrationPostgresqlCancelWhileSavingTheGlobalsKeepsOnlyTheDatabaseSnapshot(t *testing.T) {
	r, h := newRepo(t), pgTools(t, archiveOf(1000), `printf 'CREATE ROLE app_owner;'`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sum, err := h.source(t).Backup(ctx, h.config(true, nil), &cancelOnSecond{Repository: r.cli, cancel: cancel}, stepTags, &reporter{})
	if err == nil || ctx.Err() == nil {
		t.Fatalf("err = %v", err)
	}
	snaps := r.snapshots(t)
	if len(snaps) != 1 || snaps[0].Paths[0] != "/app.dump" || sum.SnapshotID != snaps[0].ID {
		t.Errorf("snapshots = %+v, summary %+v", snaps, sum)
	}
	if r.count(t, "locks") != 0 {
		t.Errorf("locks left: %d", r.count(t, "locks"))
	}
}
