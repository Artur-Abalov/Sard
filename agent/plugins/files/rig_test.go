// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// --- the file system of the host

// node is one entry of the fake file system.
type node struct {
	mode   fs.FileMode
	target string // of a symbolic link
	// lstatErr, openErr and listErr are what the agent user gets from the
	// operating system.
	lstatErr, openErr, listErr error
	// gate, if set, holds Lstat until it is closed: a file system that
	// does not answer.
	gate chan struct{}
}

type fakeFS struct {
	mu      sync.Mutex
	nodes   map[string]node
	reads   []string // paths opened
	lstats  []string
	listing []string // directories whose list was read
	listN   []int    // how many names each read asked for
	opened  int
	closed  int
}

func newFS() *fakeFS { return &fakeFS{nodes: map[string]node{}} }

func (f *fakeFS) dir(paths ...string) *fakeFS {
	for _, p := range paths {
		f.nodes[p] = node{mode: fs.ModeDir | 0o755}
	}
	return f
}

func (f *fakeFS) file(paths ...string) {
	for _, p := range paths {
		f.nodes[p] = node{mode: 0o644}
	}
}

func (f *fakeFS) set(p string, n node) { f.nodes[p] = n }

func pathError(op, name string, errno syscall.Errno) error {
	return &fs.PathError{Op: op, Path: name, Err: errno}
}

type fakeInfo struct {
	name string
	mode fs.FileMode
}

func (i fakeInfo) Name() string      { return i.name }
func (fakeInfo) Size() int64         { return 0 }
func (i fakeInfo) Mode() fs.FileMode { return i.mode }
func (fakeInfo) ModTime() time.Time  { return time.Time{} }
func (i fakeInfo) IsDir() bool       { return i.mode.IsDir() }
func (fakeInfo) Sys() any            { return nil }

func (f *fakeFS) Lstat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	n, ok := f.nodes[name]
	f.lstats = append(f.lstats, name)
	f.mu.Unlock()
	if n.gate != nil {
		<-n.gate
	}
	switch {
	case n.lstatErr != nil:
		return nil, n.lstatErr
	case !ok:
		return nil, pathError("lstat", name, syscall.ENOENT)
	}
	return fakeInfo{filepath.Base(name), n.mode}, nil
}

func (f *fakeFS) Readlink(name string) (string, error) {
	n, ok := f.nodes[name]
	if !ok || n.mode&fs.ModeSymlink == 0 || n.target == "" {
		return "", pathError("readlink", name, syscall.EINVAL)
	}
	return n.target, nil
}

type fakeFile struct {
	fs   *fakeFS
	name string
	err  error
}

func (f *fakeFS) Open(name string) (files.File, error) {
	n := f.nodes[name]
	if n.openErr != nil {
		return nil, n.openErr
	}
	f.mu.Lock()
	f.reads = append(f.reads, name)
	f.opened++
	f.mu.Unlock()
	return &fakeFile{fs: f, name: name, err: n.listErr}, nil
}

func (f *fakeFile) Readdirnames(n int) ([]string, error) {
	f.fs.mu.Lock()
	f.fs.listing = append(f.fs.listing, f.name)
	f.fs.listN = append(f.fs.listN, n)
	f.fs.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return nil, io.EOF
}

func (f *fakeFile) Close() error {
	f.fs.mu.Lock()
	f.fs.closed++
	f.fs.mu.Unlock()
	return nil
}

// --- restic

// fakeRestic stands in for the restic binary: `cat config` answers with the
// repository id, `backup` runs the script of the test.
type fakeRestic struct {
	t     *testing.T
	mu    sync.Mutex
	calls []restic.Command
	// backup replaces the default run of `restic backup`: a snapshot that
	// covers the paths. It prints through cmd and returns the exit code.
	backup func(ctx context.Context, cmd restic.Command) int
	// startErr makes `restic backup` fail to start.
	startErr error
}

const (
	repoID     = "0fa4b1261960f1b5334694f4603c60dd52a5c2b2c8f262aa6c31a7fd5b11e406"
	snapshotID = "6c719cbc95d86f49b63cf9535bad29ad721287277e8bc419bc7168a82fd1ad01"
)

func summaryLine() string {
	return `{"message_type":"summary","files_new":3,"data_added":2000,"data_added_packed":1500,"total_bytes_processed":1024,"snapshot_id":"` + snapshotID + `"}`
}

func statusLine(bytesDone, filesDone uint64) string {
	return fmt.Sprintf(`{"message_type":"status","total_files":9,"files_done":%d,"total_bytes":1024,"bytes_done":%d}`, filesDone, bytesDone)
}

func errorLine(item, during string) string {
	line, _ := json.Marshal(map[string]any{
		"message_type": "error", "error": map[string]string{"message": "open " + item + ": permission denied"},
		"during": during, "item": item,
	})
	return string(line)
}

func exitLine(code int, msg string) string {
	return fmt.Sprintf(`{"message_type":"exit_error","code":%d,"message":%q}`, code, msg)
}

func (f *fakeRestic) Run(ctx context.Context, cmd restic.Command) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	switch cmd.Args[0] {
	case "cat":
		cmd.Stdout([]byte(`{"version":2,"id":"` + repoID + `","chunker_polynomial":"3a"}`))
		return 0, nil
	case "backup":
		if f.startErr != nil {
			return -1, f.startErr
		}
		if f.backup == nil {
			cmd.Stdout([]byte(summaryLine()))
			return 0, nil
		}
		return f.backup(ctx, cmd), nil
	}
	f.t.Errorf("unexpected restic %q", cmd.Args)
	return 1, nil
}

func (f *fakeRestic) ran() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// backupArgs are the arguments of the `restic backup` run.
func (f *fakeRestic) backupArgs(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.Args[0] == "backup" {
			return c.Args
		}
	}
	t.Fatal("restic backup did not run")
	return nil
}

// script makes `restic backup` print stdout lines, then stderr lines, and exit.
func script(stdout []string, stderr []string, code int) func(context.Context, restic.Command) int {
	return func(_ context.Context, cmd restic.Command) int {
		for _, l := range stderr {
			cmd.Stderr([]byte(l))
		}
		for _, l := range stdout {
			cmd.Stdout([]byte(l))
		}
		return code
	}
}

// --- the sink and the clock

// sink collects what the executor reports.
type sink struct {
	mu       sync.Mutex
	progress []*agentv1.StepProgress
	logs     []string
	results  chan *agentv1.StepResult
}

func (s *sink) Progress(p *agentv1.StepProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = append(s.progress, p)
}

func (s *sink) Result(r *agentv1.StepResult) { s.results <- r }

func (s *sink) Log(_ string, l *agentv1.LogLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, l.GetText())
}

func (s *sink) phases() []agentv1.StepPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agentv1.StepPhase
	for _, p := range s.progress {
		if len(out) == 0 || out[len(out)-1] != p.GetPhase() {
			out = append(out, p.GetPhase())
		}
	}
	return out
}

func (s *sink) reports(phase agentv1.StepPhase) []*agentv1.StepProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*agentv1.StepProgress
	for _, p := range s.progress {
		if p.GetPhase() == phase {
			out = append(out, p)
		}
	}
	return out
}

// waitFor polls until cond holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// tickClock moves an hour at every reading, so the executor's progress
// interval never thins out the reports of a test; timers are real.
type tickClock struct{ n atomic.Int64 }

func (c *tickClock) Now() time.Time {
	return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC).Add(time.Duration(c.n.Add(1)) * time.Hour)
}

func (*tickClock) AfterFunc(d time.Duration, f func()) executor.Timer { return time.AfterFunc(d, f) }

// --- the rig

type rig struct {
	t          *testing.T
	fs         *fakeFS
	restic     *fakeRestic
	exec       *executor.Executor
	sink       *sink
	stateDir   string
	restoreDir string
}

// tree is what the fingerprint reads: no file, the rig never reads the host's.
func (r *rig) tree(string) fs.FS { return fstest.MapFS{} }

func newRig(t *testing.T) *rig { return newRigAs(t, role.Source) }

// newRigAs is a rig whose agent has that role.
func newRigAs(t *testing.T, role role.Role) *rig {
	t.Helper()
	r := &rig{t: t, fs: newFS(), sink: &sink{results: make(chan *agentv1.StepResult, 8)}}
	r.restic = &fakeRestic{t: t}
	repo := restic.New(restic.Options{
		Binary:   "/opt/sard/restic",
		CacheDir: "/var/cache/sard/restic",
		Path:     "/usr/bin:/bin",
		Exec:     r.restic,
		Keys:     fakeKeys{},
		ReadFile: func(string) ([]byte, error) { return nil, fs.ErrNotExist },
	}, config.Repository{Name: "R", URL: "/srv/restic/R", PasswordFile: "/etc/sard/R.pass"})

	reg, err := sdk.NewRegistry(files.Plugin{AgentVersion: "1.2.3", FS: r.fs, Tree: r.tree})
	if err != nil {
		t.Fatal(err)
	}
	r.restoreDir = filepath.Join(t.TempDir(), "restore")
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil, nil),
		func(name, _ string, stderr io.Writer) (restic.Repository, bool) {
			return repo.WithStderr(stderr), name == "R"
		}, r.restoreDir)
	if err != nil {
		t.Fatal(err)
	}
	r.stateDir = filepath.Join(t.TempDir(), "state")
	r.exec, err = executor.New(executor.Options{
		Role:         role,
		Handlers:     handlers,
		Sink:         r.sink,
		StateDir:     r.stateDir,
		Repositories: []string{"R"},
		MaxParallel:  2,
		CancelGrace:  time.Second,
		Clock:        &tickClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.exec.Close(context.Background()) })
	return r
}

type fakeKeys struct{}

func (fakeKeys) Name() string { return "fake" }
func (fakeKeys) RepositoryKey(context.Context, string) (crypto.Key, error) {
	return crypto.Key{Env: []string{"RESTIC_PASSWORD_FILE=/etc/sard/R.pass"}}, nil
}

// step is a files BACKUP step of repository R with the tags of the spec.
func step(cfg string) *agentv1.RunStep {
	return &agentv1.RunStep{
		CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "R",
		Tags: map[string]string{"source": "s1", "run": "r1"}, ConfigJson: cfg,
	}
}

func withTimeout(s *agentv1.RunStep, d time.Duration) *agentv1.RunStep {
	s.Timeout = durationpb.New(d)
	return s
}

// paths is the config with these paths.
func paths(p ...string) string {
	quoted := make([]string, len(p))
	for i, s := range p {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return `{"paths": [` + strings.Join(quoted, ", ") + `]}`
}

// run submits the step and waits for its result.
func (r *rig) run(s *agentv1.RunStep) *agentv1.StepResult {
	r.t.Helper()
	r.exec.Submit(s)
	return r.result()
}

func (r *rig) result() *agentv1.StepResult {
	r.t.Helper()
	select {
	case res := <-r.sink.results:
		return res
	case <-time.After(15 * time.Second):
		r.t.Fatal("no step result")
		return nil
	}
}

const (
	succeeded = agentv1.StepStatus_STEP_STATUS_SUCCEEDED
	failed    = agentv1.StepStatus_STEP_STATUS_FAILED
	rejected  = agentv1.StepStatus_STEP_STATUS_REJECTED
	cancelled = agentv1.StepStatus_STEP_STATUS_CANCELLED
	timedOut  = agentv1.StepStatus_STEP_STATUS_TIMED_OUT
)

// want checks the status of a result and, if there is one, its message.
func want(t *testing.T, res *agentv1.StepResult, status agentv1.StepStatus) {
	t.Helper()
	if res.GetStatus() != status {
		t.Fatalf("status = %v (%q), want %v", res.GetStatus(), res.GetMessage(), status)
	}
}

// noOutput: "Результат без вывода".
func noOutput(t *testing.T, res *agentv1.StepResult) {
	t.Helper()
	if res.GetOutput() != nil {
		t.Errorf("output = %v", res.GetOutput())
	}
}

func mentions(t *testing.T, msg string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(msg, p) {
			t.Errorf("message %q does not mention %q", msg, p)
		}
	}
}

func omits(t *testing.T, msg string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(msg, p) {
			t.Errorf("message %q mentions %q", msg, p)
		}
	}
}

func oneLine(t *testing.T, msg string) {
	t.Helper()
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("message is not one line: %q", msg)
	}
}
