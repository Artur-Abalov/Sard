// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

// Integration tests run the pinned restic fetched by scripts/fetch-restic.sh
// against a local repository in temporary directories:
//
//	go test -tags integration ./internal/restic/...
//
// SARD_IT_SIZE_MB sets the size of the backed-up data (default 16), e.g.
// 300 with -v to watch the progress callback.
package restic_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

type realRepo struct {
	cli  *restic.CLI
	dir  string // the repository
	data string // what is backed up
}

func pinnedBinary(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "..", ".bin", "restic", restic.Pinned.String(), "linux_"+runtime.GOARCH, "restic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned restic missing, run scripts/fetch-restic.sh: %v", err)
	}
	return bin
}

func newRealRepo(t *testing.T) realRepo {
	t.Helper()
	tmp := t.TempDir()
	password := filepath.Join(tmp, "password")
	if err := os.WriteFile(password, []byte("integration test password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := realRepo{dir: filepath.Join(tmp, "repo"), data: filepath.Join(tmp, "data")}
	repo := config.Repository{Name: "it", URL: r.dir, PasswordFile: password}
	r.cli = restic.New(restic.Options{
		Binary:   pinnedBinary(t),
		CacheDir: filepath.Join(tmp, "cache"),
		Path:     os.Getenv("PATH"),
		Exec:     restic.ProcessExecutor{},
		Keys:     crypto.NewResticAES(map[string]string{"it": password}),
		ReadFile: os.ReadFile,
		OnStderr: func(line string) { t.Logf("restic stderr: %s", line) },
	}, repo)
	return r
}

// writeData fills dir with pseudo-random files, which do not compress or
// deduplicate, and returns their total size.
func writeData(t *testing.T, dir string, sizeMB int) uint64 {
	t.Helper()
	rng := rand.NewChaCha8([32]byte{1})
	sizes := map[string]int{"small.txt": 11, "empty": 0, "nested/deep/file.bin": 1 << 20}
	for i := range 4 {
		sizes["big"+strconv.Itoa(i)+".bin"] = sizeMB << 20 / 4
	}
	var total uint64
	for name, size := range sizes {
		path := filepath.Join(dir, name)
		content := make([]byte, size)
		_, _ = rng.Read(content)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
		total += uint64(size)
	}
	return total
}

func dataSizeMB(t *testing.T) int {
	s := os.Getenv("SARD_IT_SIZE_MB")
	if s == "" {
		return 16
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 4 {
		t.Fatalf("SARD_IT_SIZE_MB = %q", s)
	}
	return n
}

func TestIntegrationPinnedVersion(t *testing.T) {
	r := newRealRepo(t)
	if v, err := r.cli.Version(context.Background()); err != nil || v != restic.Pinned {
		t.Fatalf("Version = %v, %v; pinned %v", v, err, restic.Pinned)
	}
}

// Init → Backup → Restore into another directory → byte-for-byte equal.
// A second backup of the same data adds almost nothing; the id is stable.
func TestIntegrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := newRealRepo(t)
	id, err := r.cli.Init(ctx)
	if err != nil || id == "" {
		t.Fatalf("Init = %q, %v", id, err)
	}
	total := writeData(t, r.data, dataSizeMB(t))
	first := firstBackup(t, r, id, total)
	secondBackup(t, r, first)

	target := t.TempDir()
	if err := r.cli.Restore(ctx, first.SnapshotID, target); err != nil {
		t.Fatal(err)
	}
	assertSameTree(t, r.data, filepath.Join(target, r.data))
	assertStableID(t, r, id)
}

// firstBackup stores everything; progress only grows.
func firstBackup(t *testing.T, r realRepo, id string, total uint64) restic.BackupSummary {
	t.Helper()
	var last restic.Progress
	sum, err := r.cli.Backup(context.Background(), restic.BackupRequest{Paths: []string{r.data}, Tags: []string{"it"}}, func(p restic.Progress) {
		t.Logf("progress: %d/%d bytes, %d/%d files, %.1f%%", p.BytesDone, p.TotalBytes, p.FilesDone, p.TotalFiles, 100*p.PercentDone)
		if p.BytesDone < last.BytesDone {
			t.Errorf("progress went back: %+v after %+v", p, last)
		}
		last = p
	})
	if err != nil || sum.SnapshotID == "" || sum.RepositoryID != id || sum.TotalBytes != total || sum.AddedBytes < total/2 {
		t.Fatalf("first backup = %+v, %v (data %d bytes)", sum, err, total)
	}
	t.Logf("first backup: %+v", sum)
	return sum
}

// secondBackup of unchanged data adds only tree metadata.
func secondBackup(t *testing.T, r realRepo, first restic.BackupSummary) {
	t.Helper()
	sum, err := r.cli.Backup(context.Background(), restic.BackupRequest{Paths: []string{r.data}}, nil)
	if err != nil || sum.SnapshotID == first.SnapshotID || sum.FilesUnmodified != 7 || sum.AddedBytes > 16<<10 {
		t.Fatalf("second backup = %+v, %v", sum, err)
	}
	t.Logf("second backup added %d bytes (%d before compression)", sum.AddedBytes, sum.AddedBytesRaw)
}

func assertStableID(t *testing.T, r realRepo, id string) {
	t.Helper()
	for range 2 {
		if again, err := r.cli.ID(context.Background()); again != id || err != nil {
			t.Fatalf("ID = %q, %v; Init gave %q", again, err, id)
		}
	}
}

func assertSameTree(t *testing.T, want, got string) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(want, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(want, path)
		a, errA := os.ReadFile(path)
		b, errB := os.ReadFile(filepath.Join(got, rel))
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Errorf("%s differs after restore (%v, %v)", rel, errA, errB)
		}
		files++
		return nil
	})
	if err != nil || files != 7 {
		t.Fatalf("compared %d files: %v", files, err)
	}
}

func TestIntegrationWrongPassword(t *testing.T) {
	ctx := context.Background()
	r := newRealRepo(t)
	if _, err := r.cli.Init(ctx); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("not the password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong := restic.New(restic.Options{
		Binary:   pinnedBinary(t),
		CacheDir: t.TempDir(),
		Exec:     restic.ProcessExecutor{},
		Keys:     crypto.NewResticAES(map[string]string{"it": other}),
		ReadFile: os.ReadFile,
	}, config.Repository{Name: "it", URL: r.dir, PasswordFile: other})
	if _, err := wrong.ID(ctx); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

// Cancelling a running backup stops restic, which removes its lock.
func TestIntegrationCancelledBackup(t *testing.T) {
	r := newRealRepo(t)
	if _, err := r.cli.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeData(t, r.data, 256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := r.cli.Backup(ctx, restic.BackupRequest{Paths: []string{r.data}}, func(restic.Progress) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	locks, err := os.ReadDir(filepath.Join(r.dir, "locks"))
	if err != nil || len(locks) != 0 {
		t.Fatalf("locks = %v, %v", locks, err)
	}
	if pids := running(pinnedBinary(t)); len(pids) != 0 {
		t.Fatalf("restic still running: %v", pids)
	}
}

// running lists live (non-zombie) processes of the executable bin.
func running(bin string) []string {
	var pids []string
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe")); err == nil && exe == bin && alive(pid) {
			pids = append(pids, e.Name())
		}
	}
	return pids
}
