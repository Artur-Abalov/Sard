// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// reads is the plugin T that reads the file of the snapshot into the buffer.
func reads(path string, got *bytes.Buffer) *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		return sdk.RestoreResult{}, sdk.AtStage(h.Dump(ctx, req.SnapshotID, path, got), "restore")
	}}
}

// Scenario: Поток снимка отдаёт файл снимка байт в байт без записи на диск.
func TestTheStreamOfASnapshotGivesTheFileByteForByteAndWritesNothingToDisk(t *testing.T) {
	var got bytes.Buffer
	r := newOwnRig(t, reads("/srv/data/a.txt", &got))
	content := []byte("PGDMP\r\n\x00\xff tail")
	var asked []string
	r.repo.dump = func(_ context.Context, id, path string, w io.Writer) error {
		asked = append(asked, id+" "+path)
		_, err := w.Write(content)
		return err
	}
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), content) {
		t.Errorf("the plugin got %q", got.Bytes())
	}
	if want := []string{fullID + " /srv/data/a.txt"}; !slices.Equal(asked, want) {
		t.Errorf("restic dump of %q, want %q", asked, want)
	}
	for _, dir := range []string{r.state, filepath.Dir(r.dir)} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("%s holds %v (%v)", dir, entries, err)
		}
	}
}

// snapshotsOf is the plugin T that lists the snapshots into got.
func snapshotsOf(got *[]sdk.SnapshotInfo) *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		var err error
		*got, err = h.Snapshots(ctx)
		return sdk.RestoreResult{}, sdk.AtStage(err, "check")
	}}
}

// Scenario: Список снимков — только снимки репозитория шага.
func TestTheListOfSnapshotsIsThatOfTheRepositoryOfTheStep(t *testing.T) {
	var got []sdk.SnapshotInfo
	r := newOwnRig(t, snapshotsOf(&got))
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.lookedUp(), []string{"main"}) {
		t.Errorf("repositories = %q", r.lookedUp())
	}
	want := []sdk.SnapshotInfo{{ID: fullID, Time: time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), Tags: []string{"sard.source=s1", "x.part=main"}, Paths: []string{"/main.bin"}}}
	if len(got) != 1 || got[0].ID != want[0].ID || !got[0].Time.Equal(want[0].Time) || !slices.Equal(got[0].Tags, want[0].Tags) || !slices.Equal(got[0].Paths, want[0].Paths) {
		t.Errorf("snapshots = %+v, want %+v", got, want)
	}
}

// Scenario: Ошибка хоста в потоке снимка проходит через плагин без изменений.
func TestAFailureOfTheHostInTheStreamPassesThroughThePluginUnchanged(t *testing.T) {
	const hint = "run `sudo sard-agent check` on the host"
	tests := []struct {
		name   string
		fail   error
		reason string
		hinted bool
	}{
		{"storage stops answering", restic.ErrNetwork, "BACKEND_UNAVAILABLE", true},
		{"wrong password", restic.ErrWrongPassword, "WRONG_PASSWORD", true},
		{"exit code 1 with Fatal", &restic.ExitError{Code: 1, Message: "Fatal: it broke"}, "RESTIC_FAILED", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bytes.Buffer
			r := newOwnRig(t, reads("/srv/data/a.txt", &got))
			r.repo.dump = func(context.Context, string, string, io.Writer) error { return tt.fail }
			_, err := r.run(r.step())
			if err == nil || !strings.HasPrefix(err.Error(), tt.reason+" (restore)") {
				t.Fatalf("err = %v", err)
			}
			if lines := strings.Split(err.Error(), "\n"); tt.hinted != (lines[len(lines)-1] == hint) {
				t.Errorf("hint: %v, message %q", tt.hinted, err)
			}
			if !tt.hinted && strings.Contains(err.Error(), "sard-agent check") {
				t.Errorf("message %q", err)
			}
		})
	}
}

// Scenario: Ошибка хоста в списке снимков проходит через плагин без изменений.
func TestAFailureOfTheHostInTheListOfSnapshotsPassesThroughThePluginUnchanged(t *testing.T) {
	var got []sdk.SnapshotInfo
	exec, s := cliRig(t, snapshotsOf(&got), printing{stderr: "Fatal: unable to open repository: dial tcp: connection refused", code: 1})
	exec.Submit(ownStep())
	res := s.result(t)
	if res.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED || !strings.HasPrefix(res.GetMessage(), "BACKEND_UNAVAILABLE (check)") {
		t.Fatalf("result = %v", res)
	}
}

// printing plays a restic that prints a line to stderr and exits.
type printing struct {
	stderr string
	code   int
}

func (p printing) Run(_ context.Context, c restic.Command) (int, error) {
	c.Stderr([]byte(p.stderr))
	return p.code, nil
}

// Scenario: Получатель, переставший читать, останавливает поток.
func TestAReceiverThatStopsReadingStopsTheStream(t *testing.T) {
	broken := errors.New("receiver is full")
	var seen error
	p := &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		seen = h.Dump(ctx, req.SnapshotID, "/a", failingWriter{broken})
		return sdk.RestoreResult{}, nil
	}}
	r := newOwnRig(t, p)
	stopped := false
	r.repo.dump = func(ctx context.Context, _, _ string, w io.Writer) error {
		if _, err := w.Write([]byte("first")); err != nil {
			// restic does not notice its stdout is gone: the host must stop it (SIGTERM).
			select {
			case <-ctx.Done():
				stopped = true
			case <-time.After(2 * time.Second):
			}
			return errors.New("restic dump: write: broken pipe")
		}
		return nil
	}
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	var re *sdk.RestoreError
	if !stopped || !errors.Is(seen, broken) || errors.As(seen, &re) {
		t.Errorf("stopped: %v, the plugin got %v", stopped, seen)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// Scenario: Остановка потока собственным контекстом плагина не прерывает шаг.
func TestAStreamStoppedByTheContextOfThePluginDoesNotInterruptTheStep(t *testing.T) {
	var second bytes.Buffer
	first := make(chan error, 1)
	p := &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		own, cancel := context.WithCancel(ctx)
		defer cancel()
		first <- h.Dump(own, req.SnapshotID, "/toc", &cancelAfterWrite{cancel: cancel})
		if err := h.Dump(ctx, req.SnapshotID, "/data", &second); err != nil {
			return sdk.RestoreResult{}, err
		}
		return sdk.RestoreResult{Database: "x1"}, nil
	}}
	r := newOwnRig(t, p)
	var terminated []string
	r.repo.dump = func(ctx context.Context, _, path string, w io.Writer) error {
		_, _ = w.Write([]byte("bytes of " + path))
		if path != "/toc" {
			return nil
		}
		<-ctx.Done() // SIGTERM of restic: the context of the plugin ended
		terminated = append(terminated, path)
		return ctx.Err()
	}
	res, rep, err := r.runReported(r.step())
	if err != nil {
		t.Fatal(err)
	}
	if res.GetRestore().GetDatabase() != "x1" || second.String() != "bytes of /data" || !slices.Equal(terminated, []string{"/toc"}) {
		t.Errorf("result %v, second stream %q, terminated %q", res, second.String(), terminated)
	}
	if stopped := <-first; !errors.Is(stopped, context.Canceled) {
		t.Errorf("the first stream ended with %v", stopped)
	}
	assertNoInterruption(t, append(rep.texts(), r.logs.String()))
}

func assertNoInterruption(t *testing.T, texts []string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, "INTERRUPTED") {
			t.Errorf("INTERRUPTED in %q", text)
		}
	}
}

// cancelAfterWrite cancels the context of the plugin once the first bytes came.
type cancelAfterWrite struct{ cancel context.CancelFunc }

func (c *cancelAfterWrite) Write(p []byte) (int, error) {
	c.cancel()
	return len(p), nil
}

// blockingRestic plays a restic that does not end before it is stopped.
type blockingRestic struct {
	mu      sync.Mutex
	started chan struct{}
	args    []string
	stopped bool
}

func (b *blockingRestic) Run(ctx context.Context, c restic.Command) (int, error) {
	b.mu.Lock()
	b.args = c.Args
	b.mu.Unlock()
	if c.StdoutTo != nil {
		_, _ = c.StdoutTo.Write([]byte("first bytes"))
	}
	close(b.started)
	<-ctx.Done() // SIGTERM to the group; SIGKILL after 10 s is the executor's of restic
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	return -1, ctx.Err()
}

// streamRig is a host whose repository is the real wrapper of restic over a restic that blocks.
func streamRig(t *testing.T, p sdk.Plugin) (*executor.Executor, *sink, *blockingRestic) {
	t.Helper()
	fake := &blockingRestic{started: make(chan struct{})}
	exec, s := cliRig(t, p, fake)
	return exec, s, fake
}

// cliRig is a host whose repository is the real wrapper of restic over the process given.
func cliRig(t *testing.T, p sdk.Plugin, process restic.Executor) (*executor.Executor, *sink) {
	t.Helper()
	reg, err := sdk.NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	cli := restic.New(restic.Options{
		Exec: process, Keys: crypto.NewResticAES(map[string]string{"main": "/etc/sard/main.pass"}), Logger: slog.New(slog.DiscardHandler),
	}, config.Repository{Name: "main", URL: "/srv/restic/main", PasswordFile: "/etc/sard/main.pass"})
	attrs := hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}
	engine := consolerestore.New(consolerestore.Options{Dir: filepath.Join(t.TempDir(), "restore"), FS: hostsetup.OS{}, Owner: attrs, Logger: slog.New(slog.DiscardHandler)})
	state := t.TempDir()
	store := restorejournal.New(restorejournal.Options{Dir: filepath.Join(state, "restore-journal"), FS: hostsetup.OS{}, Owner: attrs})
	repos := func(string, string, io.Writer) (restic.Repository, bool) { return cli, true }
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil), repos, t.TempDir(), pluginhost.WithConsoleRestore(engine), pluginhost.WithRestoreJournal(pluginhost.NewJournal(store, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := newSink()
	exec, err := executor.New(executor.Options{Role: role.Source, Handlers: handlers, Sink: s, StateDir: state + "/exec", Repositories: []string{"main"}, CancelGrace: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	return exec, s
}

func streaming() *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		var got bytes.Buffer
		return sdk.RestoreResult{}, h.Dump(ctx, req.SnapshotID, "/srv/data/a.txt", &got)
	}}
}

func ownStep() *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", Plugin: "journaltest", Action: restore, RepositoryName: "main", SnapshotId: fullID, ConfigJson: "{}"}
}

// Scenario: Отмена шага во время потока снимка останавливает restic и даёт CANCELLED.
func TestCancellingTheStepDuringTheStreamStopsResticAndIsCancelled(t *testing.T) {
	exec, s, fake := streamRig(t, streaming())
	exec.Submit(ownStep())
	<-fake.started
	exec.Cancel("c1")
	res := s.result(t)
	if res.GetStatus() != agentv1.StepStatus_STEP_STATUS_CANCELLED {
		t.Fatalf("result = %v", res)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.stopped || !slices.Contains(fake.args, "dump") || !slices.Contains(fake.args, fullID) {
		t.Errorf("restic stopped: %v, args %q", fake.stopped, fake.args)
	}
}

// Scenario: Таймаут шага во время потока снимка даёт TIMED_OUT. The timeout is
// 100 ms here, not 2 s, to keep the test short; the grace of restic is its own.
func TestTheTimeoutOfTheStepDuringTheStreamIsTimedOutAndResticDoesNotStayRunning(t *testing.T) {
	exec, s, fake := streamRig(t, streaming())
	step := ownStep()
	step.Timeout = durationpb.New(100 * time.Millisecond)
	exec.Submit(step)
	res := s.result(t)
	if res.GetStatus() != agentv1.StepStatus_STEP_STATUS_TIMED_OUT {
		t.Fatalf("result = %v", res)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.stopped {
		t.Error("restic is still running")
	}
}
