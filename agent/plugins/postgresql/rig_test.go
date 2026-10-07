// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// P is "Секрет pg-app": a quote, a colon, a backslash, a space and a
// non-ASCII letter.
const P = `p'a:s\s w0rd-Ж`

const (
	repoID   = "0fa4b1261960f1b5334694f4603c60dd52a5c2b2c8f262aa6c31a7fd5b11e406"
	secretFn = "/etc/sard/pg-app"
)

// --- the file system of the host

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

// fakeFS holds the tools of the host: path → mode.
type fakeFS struct {
	mu    sync.Mutex
	nodes map[string]fs.FileMode
	stats []string
	// errs are the errors Stat answers with instead of looking at nodes.
	errs map[string]error
}

func (f *fakeFS) Stat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats = append(f.stats, name)
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	mode, ok := f.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: syscall.ENOENT}
	}
	return fakeInfo{filepath.Base(name), mode}, nil
}

// install puts executables of the tools into dir.
func (f *fakeFS) install(dir string, tools ...string) {
	for _, tool := range tools {
		f.nodes[dir+"/"+tool] = 0o755
	}
}

// --- the processes of the host

// phaseNow reports the phase the step is in.
type phaseNow func() agentv1.StepPhase

// call is one process the plugin started.
type call struct {
	Tool  string // base name of the executable
	Path  string
	Args  []string
	Env   []string
	Phase agentv1.StepPhase
}

func (c call) version() bool { return slices.Equal(c.Args, []string{"--version"}) }

// key is how a test names the behaviour of a call: "psql", "pg_dump",
// "pg_dump --version" ...
func (c call) key() string {
	if c.version() {
		return c.Tool + " --version"
	}
	return c.Tool
}

// env is the value of an environment variable of the process.
func (c call) env(name string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// arg is the value of --name=value among the arguments.
func (c call) arg(name string) string {
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

// behaviour is what a fake process does.
type behaviour func(ctx context.Context, c plugCmd) (int, error)

type plugCmd = postgresql.Cmd

// fakeRunner stands in for the host's processes.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []call
	phase  phaseNow
	script map[string]behaviour
}

func (f *fakeRunner) Run(ctx context.Context, c postgresql.Cmd) (int, error) {
	k := call{Tool: filepath.Base(c.Path), Path: c.Path, Args: slices.Clone(c.Args), Env: slices.Clone(c.Env), Phase: f.phase()}
	f.mu.Lock()
	f.calls = append(f.calls, k)
	run, ok := f.script[k.key()]
	f.mu.Unlock()
	if !ok {
		return -1, fmt.Errorf("fake: no behaviour for %q", k.key())
	}
	return run(ctx, c)
}

func (f *fakeRunner) on(key string, b behaviour) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[key] = b
}

// ran returns the calls of a tool; the version queries are separate keys.
func (f *fakeRunner) ran(key string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.key() == key {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeRunner) started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// say is a process that prints stdout, writes stderr lines and exits.
func say(stdout string, stderr []string, code int) behaviour {
	return func(_ context.Context, c plugCmd) (int, error) {
		for _, l := range stderr {
			if c.Stderr != nil {
				c.Stderr(l)
			}
		}
		if c.Stdout != nil {
			if _, err := io.WriteString(c.Stdout, stdout); err != nil {
				return -1, errors.New("signal: terminated")
			}
		}
		return code, nil
	}
}

// fail is a process that writes a line to stderr and exits with code 1.
func fail(line string) behaviour { return say("", []string{line}, 1) }

// archive is the output of a custom-format pg_dump of n bytes.
func archive(n int) string { return "PGDMP" + strings.Repeat("x", n-5) }

const (
	pgDumpVersion = "pg_dump (PostgreSQL) 18.0\n"
	pgDumpall     = "pg_dumpall (PostgreSQL) 18.0\n"
	globalsSQL    = "CREATE ROLE app_owner;\n"
)

// psqlRow is the answer of the query of PREPARING: server_version_num,
// is_superuser, member of pg_read_all_data, the role.
func psqlRow(num int, super, member bool, role string) string {
	yn := func(b bool) string {
		if b {
			return "t"
		}
		return "f"
	}
	return fmt.Sprintf("%d|%s|%s|%s\n", num, yn(super), yn(member), role)
}

func newRunner(phase phaseNow) *fakeRunner {
	f := &fakeRunner{phase: phase, script: map[string]behaviour{}}
	f.script["pg_dump --version"] = say(pgDumpVersion, nil, 0)
	f.script["pg_dumpall --version"] = say(pgDumpall, nil, 0)
	f.script["psql"] = say(psqlRow(180000, false, true, "backup"), nil, 0)
	f.script["pg_dump"] = say(archive(1<<20), nil, 0)
	f.script["pg_dumpall"] = say(globalsSQL, nil, 0)
	return f
}

// --- restic

// fakeRestic stands in for the restic binary: `cat config` answers with the
// repository id, `backup --stdin` reads its stdin to the end and stores it as
// the next snapshot.
type fakeRestic struct {
	t   *testing.T
	mu  sync.Mutex
	run []restic.Command
	// stdin is what each backup read.
	stdin []string
	// backup replaces the default run of `restic backup`; n counts from 0.
	backup func(ctx context.Context, n int, cmd restic.Command) int
	// bytesDone are the progress statuses the default run reports.
	bytesDone []uint64
	// hold keeps the default run of backup n from reading its stdin until
	// restic is stopped: a backup that takes long.
	hold func(n int) bool
}

func snapshotID(n int) string { return fmt.Sprintf("%064x", n+1) }

func (f *fakeRestic) Run(ctx context.Context, cmd restic.Command) (int, error) {
	f.mu.Lock()
	f.run = append(f.run, cmd)
	n := len(f.stdin)
	f.mu.Unlock()
	switch cmd.Args[0] {
	case "cat":
		cmd.Stdout([]byte(`{"version":2,"id":"` + repoID + `","chunker_polynomial":"3a"}`))
		return 0, nil
	case "backup":
		if f.backup != nil {
			return f.backup(ctx, n, cmd), nil
		}
		return f.defaultBackup(ctx, n, cmd), nil
	}
	f.t.Errorf("unexpected restic %q", cmd.Args)
	return 1, nil
}

func (f *fakeRestic) record(content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stdin = append(f.stdin, string(content))
}

// slurp reads the stdin of a backup to the end.
func slurp(cmd restic.Command) []byte {
	if cmd.Stdin == nil {
		return nil
	}
	data, _ := io.ReadAll(cmd.Stdin)
	return data
}

// stopped is what restic prints when SIGTERM stops it; the agent then ends
// the stdin of restic (restic 0.19 reads it to the end).
const stopped = "signal terminated received, cleaning up"

// defaultBackup stores the stream as a snapshot once it is complete. A
// restic that is stopped before stores nothing and exits with 1.
func (f *fakeRestic) defaultBackup(ctx context.Context, n int, cmd restic.Command) int {
	for _, b := range f.bytesDone {
		cmd.Stdout([]byte(fmt.Sprintf(`{"message_type":"status","total_files":1,"files_done":0,"total_bytes":2048,"bytes_done":%d}`, b)))
	}
	type read struct{ data []byte }
	done := make(chan read, 1)
	if f.hold != nil && f.hold(n) {
		go func() { <-ctx.Done(); done <- read{slurp(cmd)} }()
	} else {
		go func() { done <- read{slurp(cmd)} }()
	}
	var data []byte
	select {
	case r := <-done:
		data = r.data
	case <-ctx.Done():
		cmd.Stderr([]byte(stopped))
		<-done
		cmd.Stderr([]byte("Fatal: unable to save snapshot: context canceled"))
		return 1
	}
	f.record(data)
	cmd.Stdout([]byte(fmt.Sprintf(`{"message_type":"summary","files_new":1,"data_added":%d,"data_added_packed":%d,"total_bytes_processed":%d,"snapshot_id":%q}`,
		len(data)+10, len(data)+5, len(data), snapshotID(n))))
	return 0
}

// backups are the `restic backup` runs.
func (f *fakeRestic) backups() []restic.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []restic.Command
	for _, c := range f.run {
		if c.Args[0] == "backup" {
			out = append(out, c)
		}
	}
	return out
}

// flag is the value of --name=value among the arguments of a restic run.
func flag(c restic.Command, name string) string {
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

// tagsOf are the --tag values of a restic run, sorted.
func tagsOf(c restic.Command) []string {
	var out []string
	for i, a := range c.Args {
		if a == "--tag" && i+1 < len(c.Args) {
			out = append(out, c.Args[i+1])
		}
	}
	slices.Sort(out)
	return out
}

// --- the sink and the clock

type sink struct {
	mu       sync.Mutex
	progress []*agentv1.StepProgress
	logs     []logLine
	results  chan *agentv1.StepResult
}

type logLine struct {
	Level agentv1.LogLevel
	Text  string
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
	s.logs = append(s.logs, logLine{l.GetLevel(), l.GetText()})
}

func (s *sink) phase() agentv1.StepPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.progress) == 0 {
		return agentv1.StepPhase_STEP_PHASE_UNSPECIFIED
	}
	return s.progress[len(s.progress)-1].GetPhase()
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

func (s *sink) bytesIn(phase agentv1.StepPhase) []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uint64
	for _, p := range s.progress {
		if p.GetPhase() == phase {
			out = append(out, p.GetBytesProcessed())
		}
	}
	return out
}

func (s *sink) lines() []logLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.logs)
}

// warnings are the WARN lines of the step's log.
func (s *sink) warnings() []string {
	var out []string
	for _, l := range s.lines() {
		if l.Level == agentv1.LogLevel_LOG_LEVEL_WARN {
			out = append(out, l.Text)
		}
	}
	return out
}

// text is the whole log of the step.
func (s *sink) text() string {
	var b strings.Builder
	for _, l := range s.lines() {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

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
	return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).Add(time.Duration(c.n.Add(1)) * time.Hour)
}

func (*tickClock) AfterFunc(d time.Duration, f func()) executor.Timer { return time.AfterFunc(d, f) }

// --- the secrets of the host

// secrets is the secret pg-app of the host: its file and what the executor
// masks.
type secrets struct {
	mu   sync.Mutex
	file []byte
}

func (s *secrets) set(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = []byte(content)
}

func (s *secrets) read(string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.file), nil
}

// For implements executor.Secrets as the agent does: the masked value is the
// content without its line ending.
func (s *secrets) For(*agentv1.RunStep) ([]executor.Secret, error) {
	content, _ := s.read("")
	return []executor.Secret{{Name: "pg-app", Value: bytes.TrimRight(content, "\r\n"), Ref: "pg-app", Content: content}}, nil
}

// --- the rig

type rig struct {
	t        *testing.T
	fs       *fakeFS
	proc     *fakeRunner
	restic   *fakeRestic
	secrets  *secrets
	env      []string
	exec     *executor.Executor
	sink     *sink
	stateDir string
	tmpDir   string
}

type fakeKeys struct{}

func (fakeKeys) Name() string { return "fake" }
func (fakeKeys) RepositoryKey(context.Context, string) (crypto.Key, error) {
	return crypto.Key{Env: []string{"RESTIC_PASSWORD_FILE=/etc/sard/R.pass"}}, nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, sink: &sink{results: make(chan *agentv1.StepResult, 8)}, secrets: &secrets{}}
	r.secrets.set(P + "\n")
	r.tmpDir = t.TempDir()
	t.Setenv("TMPDIR", r.tmpDir)
	r.fs = &fakeFS{nodes: map[string]fs.FileMode{"/usr/bin": fs.ModeDir | 0o755}}
	r.fs.install("/usr/bin", "pg_dump", "psql", "pg_dumpall")
	r.env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/var/lib/sard", "LANG=C.UTF-8"}
	r.proc = newRunner(r.sink.phase)
	r.restic = &fakeRestic{t: t}
	repo := restic.New(restic.Options{
		Binary:   "/opt/sard/restic",
		CacheDir: "/var/cache/sard/restic",
		Path:     "/usr/bin:/bin",
		Exec:     r.restic,
		Keys:     fakeKeys{},
		ReadFile: func(string) ([]byte, error) { return nil, fs.ErrNotExist },
	}, config.Repository{Name: "R", URL: "/srv/restic/R", PasswordFile: "/etc/sard/R.pass"})

	plugin := postgresql.Plugin{AgentVersion: "1.2.3", Runner: r.proc, FS: r.fs, Environ: func() []string { return slices.Clone(r.env) }}
	reg, err := sdk.NewRegistry(plugin)
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(map[string]string{"pg-app": secretFn}, r.secrets.read),
		func(name, _ string, stderr io.Writer) (restic.Repository, bool) {
			return repo.WithStderr(stderr), name == "R"
		}, filepath.Join(t.TempDir(), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	r.stateDir = filepath.Join(t.TempDir(), "state")
	r.exec, err = executor.New(executor.Options{
		Handlers:     handlers,
		Sink:         r.sink,
		Secrets:      r.secrets,
		OutputLevel:  pluginhost.OutputLevel,
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

// K is the "Конфиг K": one snapshot, no pg_dumpall.
func k(over ...map[string]any) map[string]any {
	cfg := map[string]any{"host": "db", "database": "app", "user": "backup", "password_ref": "pg-app", "tls_mode": "disable", "include_globals": false}
	for _, o := range over {
		for key, v := range o {
			if v == nil {
				delete(cfg, key)
				continue
			}
			cfg[key] = v
		}
	}
	return cfg
}

// kg is the "Конфиг KG": K without include_globals.
func kg(over ...map[string]any) map[string]any {
	return k(append([]map[string]any{{"include_globals": nil}}, over...)...)
}

func js(cfg map[string]any) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// step is the "Шаг postgresql": BACKUP into R with the tags of the spec.
func step(cfg string) *agentv1.RunStep {
	return &agentv1.RunStep{
		CommandId: "c1", Plugin: "postgresql", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "R",
		Tags: map[string]string{"sard.source": "s1", "sard.run": "r1"}, ConfigJson: cfg,
	}
}

func withTimeout(s *agentv1.RunStep, d time.Duration) *agentv1.RunStep {
	s.Timeout = durationpb.New(d)
	return s
}

func (r *rig) run(s *agentv1.RunStep) *agentv1.StepResult {
	r.t.Helper()
	r.exec.Submit(s)
	return r.result()
}

// backup runs the step of a config.
func (r *rig) backup(cfg map[string]any) *agentv1.StepResult {
	r.t.Helper()
	return r.run(step(js(cfg)))
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

	preparing = agentv1.StepPhase_STEP_PHASE_PREPARING
	dumping   = agentv1.StepPhase_STEP_PHASE_DUMPING
	uploading = agentv1.StepPhase_STEP_PHASE_UPLOADING
)

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

func oneLine(t *testing.T, msg string) {
	t.Helper()
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("message is not one line: %q", msg)
	}
}

// notDisclosed is "Пароль не раскрыт" for what the step reported.
func (r *rig) notDisclosed(res *agentv1.StepResult) {
	r.t.Helper()
	if strings.Contains(res.GetMessage(), P) || strings.Contains(r.sink.text(), P) {
		r.t.Errorf("the password is in the message %q or the log %q", res.GetMessage(), r.sink.text())
	}
	r.proc.mu.Lock()
	defer r.proc.mu.Unlock()
	for _, c := range r.proc.calls {
		for _, a := range c.Args {
			if strings.Contains(a, P) {
				r.t.Errorf("the password is in an argument of %s: %q", c.Tool, a)
			}
		}
	}
}

// filesContaining lists the files below the state and temporary directories
// that hold text.
func (r *rig) filesContaining(text string) []string {
	r.t.Helper()
	var out []string
	for _, root := range []string{r.stateDir, r.tmpDir} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte(text)) {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// errTerminated is what a process stopped by SIGTERM reports.
var (
	errTerminated = errors.New("signal: terminated")
	errKilled     = errors.New("signal: killed")
)

type resticCommand = restic.Command
