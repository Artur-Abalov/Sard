// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

// Integration tests run the test plugin through Source against the pinned
// restic (scripts/fetch-restic.sh) and a local repository:
//
//	go test -tags integration ./internal/pluginhost/...
package pluginhost_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/testplugin"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

type itRepo struct {
	cli *restic.CLI
	dir string
}

func pinnedRestic(t *testing.T) string {
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

func newRepo(t *testing.T) itRepo {
	t.Helper()
	tmp := t.TempDir()
	password := filepath.Join(tmp, "password")
	if err := os.WriteFile(password, []byte("integration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := itRepo{dir: filepath.Join(tmp, "repo")}
	r.cli = restic.New(restic.Options{
		Binary:   pinnedRestic(t),
		CacheDir: filepath.Join(tmp, "cache"),
		Path:     os.Getenv("PATH"),
		Exec:     restic.ProcessExecutor{},
		Keys:     crypto.NewResticAES(map[string]string{"it": password}),
		ReadFile: os.ReadFile,
		OnStderr: func(line string) { t.Logf("restic stderr: %s", line) },
	}, config.Repository{Name: "it", URL: r.dir, PasswordFile: password})
	if _, err := r.cli.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

// testSource is the test plugin with one secret, "token", on disk.
func testSource(t *testing.T) *pluginhost.Source {
	t.Helper()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := pluginhost.NewSource(testplugin.Plugin{}, pluginhost.NewSecrets(map[string]string{"token": token}, os.ReadFile))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func randomFile(t *testing.T, path string, size int, seed byte) {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.NewChaCha8([32]byte{seed}).Read(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// backupAndRestore runs a backup of cfg, restores the snapshot and has the
// plugin verify the restored copy.
func backupAndRestore(t *testing.T, cfg string) {
	t.Helper()
	ctx := context.Background()
	r, s := newRepo(t), testSource(t)
	var rep reporter
	sum, err := s.Backup(ctx, sdk.Config(cfg), r.cli, []string{"run=it"}, &rep)
	if err != nil || sum.SnapshotID == "" || sum.RepositoryID == "" {
		t.Fatalf("Backup = %+v, %v", sum, err)
	}
	t.Logf("summary: %+v", sum)
	if got := phases(rep.all()); !slices.Equal(got, []agentv1.StepPhase{preparing, dumping, uploading}) {
		t.Errorf("phases = %v", got)
	}
	target := t.TempDir()
	if err := r.cli.Restore(ctx, sum.SnapshotID, target); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, sdk.Config(cfg), target, &rep); err != nil {
		t.Fatalf("restored copy: %v", err)
	}
}

func TestIntegrationBackupByPathsAndRestore(t *testing.T) {
	data := t.TempDir()
	randomFile(t, filepath.Join(data, "big.bin"), 8<<20, 1)
	randomFile(t, filepath.Join(data, "nested", "deep", "small.txt"), 100, 2)
	randomFile(t, filepath.Join(data, "scratch.tmp"), 100, 3)
	backupAndRestore(t, `{"mode":"paths","source":"`+data+`","exclude":["*.tmp"],"token":"token"}`)
}

func TestIntegrationBackupAsAStreamAndRestore(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db.sql")
	randomFile(t, file, 8<<20, 4)
	backupAndRestore(t, `{"mode":"stream","source":"`+file+`","token":"token"}`)
}

// cancelling cancels the step once the stream has written 1 MiB.
type cancelling struct {
	reporter
	cancel context.CancelFunc
}

func (c *cancelling) Progress(phase agentv1.StepPhase, done, total uint64) {
	c.reporter.Progress(phase, done, total)
	if phase == uploading && done >= 1<<20 {
		c.cancel()
	}
}

// streamSpy records how the test plugin's Stream ended.
type streamSpy struct {
	testplugin.Plugin
	ended chan error
}

func (s streamSpy) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, d sdk.Dump, w io.Writer) error {
	err := s.Plugin.Stream(ctx, h, cfg, d, w)
	s.ended <- err
	return err
}

func TestIntegrationCancellingDuringTheDumpStopsTheDumpAndRestic(t *testing.T) {
	file := filepath.Join(t.TempDir(), "db.sql")
	randomFile(t, file, 64<<20, 5)
	r := newRepo(t)
	spy := streamSpy{ended: make(chan error, 1)}
	s, err := pluginhost.NewSource(spy, pluginhost.NewSecrets(nil, os.ReadFile))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = s.Backup(ctx, sdk.Config(`{"mode":"stream","source":"`+file+`"}`), r.cli, nil, &cancelling{cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := <-spy.ended; err == nil {
		t.Error("the dump ran to the end")
	}
	if snaps, _ := os.ReadDir(filepath.Join(r.dir, "snapshots")); len(snaps) != 0 {
		t.Errorf("%d snapshots stored", len(snaps))
	}
	if locks, _ := os.ReadDir(filepath.Join(r.dir, "locks")); len(locks) != 0 {
		t.Errorf("%d locks left", len(locks))
	}
	if pids := resticProcesses(pinnedRestic(t)); len(pids) != 0 {
		t.Errorf("restic still running: %v", pids)
	}
}

// phases lists the phases of events in order, without repeats.
func phases(events []event) []agentv1.StepPhase {
	var out []agentv1.StepPhase
	for _, e := range events {
		if e.phase != agentv1.StepPhase_STEP_PHASE_UNSPECIFIED && (len(out) == 0 || out[len(out)-1] != e.phase) {
			out = append(out, e.phase)
		}
	}
	return out
}

// resticProcesses lists live processes of the executable bin started by
// this test process; test binaries of other packages run in parallel.
func resticProcesses(bin string) []string {
	var pids []string
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if err != nil || exe != bin {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err == nil && ownLiveChild(stat) {
			pids = append(pids, e.Name())
		}
	}
	return pids
}

// ownLiveChild reads /proc/<pid>/stat, "pid (comm) S ppid ...": not a
// zombie and a child of this process.
func ownLiveChild(stat []byte) bool {
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	return len(fields) >= 2 && fields[0] != "Z" && fields[1] == strconv.Itoa(os.Getpid())
}
