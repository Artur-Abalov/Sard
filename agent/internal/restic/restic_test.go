// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

var _ restic.Repository = (*restic.CLI)(nil)

// The golden files are real output of the pinned restic 0.19.1; the
// repository they come from has this id.
const goldenRepoID = "0fa4b1261960f1b5334694f4603c60dd52a5c2b2c8f262aa6c31a7fd5b11e406"

// reply is what the fake restic prints for one subcommand.
type reply struct {
	stdout, stderr string // testdata file names
	inline         string // stdout given verbatim, for synthetic cases
	code           int
	err            error
	during         func() // runs while "restic" is running, e.g. cancel
	// readStdin makes the fake read stdin to EOF, as restic backup --stdin
	// does, before it replies. Like restic 0.19, a cancelled context
	// (SIGTERM) makes it confirm on stderr and keep reading until EOF,
	// then exit with 130 and no snapshot.
	readStdin bool
}

// fakeExec replays golden output instead of starting restic.
type fakeExec struct {
	t       *testing.T
	replies map[string]reply // by subcommand, e.g. "backup"
	calls   []restic.Command
	stdin   []byte // what a readStdin reply read
	killed  bool   // a readStdin reply was stopped before EOF
}

func (f *fakeExec) Run(ctx context.Context, cmd restic.Command) (int, error) {
	f.calls = append(f.calls, cmd)
	r, ok := f.replies[cmd.Args[0]]
	if !ok {
		f.t.Fatalf("unexpected restic %q", cmd.Args)
	}
	if r.readStdin && f.readStdin(ctx, cmd) {
		return 130, nil
	}
	feed(f.t, r.stdout, cmd.Stdout)
	for line := range strings.Lines(r.inline) {
		cmd.Stdout([]byte(strings.TrimSuffix(line, "\n")))
	}
	feed(f.t, r.stderr, cmd.Stderr)
	if r.during != nil {
		r.during()
	}
	return r.code, r.err
}

// readStdin reads stdin to EOF into f.stdin; true means ctx ended first.
func (f *fakeExec) readStdin(ctx context.Context, cmd restic.Command) (killed bool) {
	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(cmd.Stdin)
		done <- data
	}()
	select {
	case f.stdin = <-done:
		return false
	case <-ctx.Done():
		cmd.Stderr([]byte("signal terminated received, cleaning up    "))
		f.stdin, f.killed = <-done, true
		return true
	}
}

func (f *fakeExec) call(sub string) restic.Command {
	for _, c := range f.calls {
		if c.Args[0] == sub {
			return c
		}
	}
	f.t.Fatalf("restic %s was not run", sub)
	return restic.Command{}
}

func feed(t *testing.T, file string, fn func([]byte)) {
	t.Helper()
	if file == "" || fn == nil { // a nil callback discards the stream
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		fn([]byte(strings.TrimSuffix(line, "\n")))
	}
}

// fakeKeys hands out a password file path, as restic-aes does.
type fakeKeys struct{ err error }

func (fakeKeys) Name() string { return "fake" }
func (k fakeKeys) RepositoryKey(_ context.Context, repository string) (crypto.Key, error) {
	return crypto.Key{Env: []string{"RESTIC_PASSWORD_FILE=/etc/sard/" + repository + ".pass"}}, k.err
}

// files is an in-memory ReadFile.
type files map[string]string

func (f files) read(name string) ([]byte, error) {
	data, ok := f[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}

type fixture struct {
	exec   *fakeExec
	stderr []string
	files  files
	keys   fakeKeys
	repo   config.Repository
}

func newFixture(t *testing.T, replies map[string]reply) *fixture {
	f := &fixture{
		exec:  &fakeExec{t: t, replies: replies},
		files: files{},
		repo:  config.Repository{Name: "main", URL: "/srv/restic/main", PasswordFile: "/etc/sard/main.pass"},
	}
	return f
}

func (f *fixture) build() *restic.CLI {
	return restic.New(restic.Options{
		Binary:   "/opt/sard/restic",
		CacheDir: "/var/cache/sard/restic",
		Path:     "/usr/bin:/bin",
		Exec:     f.exec,
		Keys:     f.keys,
		ReadFile: f.files.read,
		OnStderr: func(line string) { f.stderr = append(f.stderr, line) },
	}, f.repo)
}

var catConfig = reply{stdout: "cat-config.json"}

func TestIDReadsTheRepositoryConfig(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	id, err := f.build().ID(context.Background())
	if id != goldenRepoID || err != nil {
		t.Fatalf("ID = %q, %v", id, err)
	}
	if got := f.exec.call("cat"); got.Path != "/opt/sard/restic" || !slices.Equal(got.Args, []string{"cat", "config"}) {
		t.Errorf("ran %s %q", got.Path, got.Args)
	}
}

func TestIDRejectsAConfigWithoutID(t *testing.T) {
	for name, file := range map[string]string{"no id": "version.json", "not json": "version.txt"} {
		f := newFixture(t, map[string]reply{"cat": {stdout: file}})
		if id, err := f.build().ID(context.Background()); id != "" || !errors.Is(err, restic.ErrBadOutput) {
			t.Errorf("%s: ID = %q, %v", name, id, err)
		}
	}
}

func TestInitReturnsTheNewRepositoryID(t *testing.T) {
	f := newFixture(t, map[string]reply{"init": {stdout: "init.json"}})
	id, err := f.build().Init(context.Background())
	if id != goldenRepoID || err != nil {
		t.Fatalf("Init = %q, %v", id, err)
	}
	if got := f.exec.call("init").Args; !slices.Equal(got, []string{"init", "--json"}) {
		t.Errorf("args = %q", got)
	}
}

func TestInitFailure(t *testing.T) {
	f := newFixture(t, map[string]reply{"init": {stderr: "wrong-password.stderr", code: 1}})
	var exitErr *restic.ExitError
	if id, err := f.build().Init(context.Background()); id != "" || !errors.As(err, &exitErr) {
		t.Fatalf("Init = %q, %v", id, err)
	}
}

func TestInitRejectsOutputWithoutID(t *testing.T) {
	f := newFixture(t, map[string]reply{"init": {stdout: "version.json"}})
	if id, err := f.build().Init(context.Background()); id != "" || !errors.Is(err, restic.ErrBadOutput) {
		t.Fatalf("Init = %q, %v", id, err)
	}
}

// restic's exit codes 10, 11 and 12 become sentinel errors.
func TestExitCodesBecomeSentinelErrors(t *testing.T) {
	cases := map[error]reply{
		restic.ErrNoRepository:  {stderr: "no-repository.stderr", code: 10},
		restic.ErrLocked:        {code: 11},
		restic.ErrWrongPassword: {stderr: "wrong-password.stderr", code: 12},
	}
	for want, r := range cases {
		f := newFixture(t, map[string]reply{"cat": r})
		_, err := f.build().ID(context.Background())
		if !errors.Is(err, want) || err.Error() != "restic cat: "+want.Error() {
			t.Errorf("code %d: err = %v", r.code, err)
		}
	}
}

// Any other exit code carries the code and restic's fatal message.
func TestOtherExitCodesCarryResticsMessage(t *testing.T) {
	cases := map[string]reply{
		"restic cat: exit code 1: Fatal: wrong password or no key found": {stderr: "wrong-password.stderr", code: 1},
		`restic cat: exit code 1: Fatal: failed to find snapshot: no matching ID found for prefix "deadbeef"`: {
			stderr: "restore-missing.stderr", code: 1,
		},
		"restic cat: exit code -1": {code: -1},
	}
	for want, r := range cases {
		f := newFixture(t, map[string]reply{"cat": r})
		_, err := f.build().ID(context.Background())
		var exitErr *restic.ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != r.code || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

func TestExecutorFailureIsReturned(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {code: -1, err: fs.ErrNotExist}})
	if _, err := f.build().ID(context.Background()); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "restic cat: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestStderrLinesReachTheCallback(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {stderr: "no-repository.stderr", code: 10}})
	_, _ = f.build().ID(context.Background())
	want := []string{
		"Fatal: repository does not exist: unable to open config file: stat /tmp/sardcap2/norepo/config: no such file or directory",
		"Is there a repository at the following location?",
		"/tmp/sardcap2/repo/../norepo",
	}
	if !slices.Equal(f.stderr, want) {
		t.Fatalf("stderr = %q", f.stderr)
	}
}

func TestStderrCallbackIsOptional(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {stderr: "wrong-password.stderr", code: 12}})
	cli := restic.New(restic.Options{Exec: f.exec, Keys: f.keys, ReadFile: f.files.read}, f.repo)
	if _, err := cli.ID(context.Background()); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

// The restic process gets exactly the environment the wrapper builds: no
// RESTIC_* variable or cloud credential of the agent's own environment.
func TestResticDoesNotInheritTheAgentEnvironment(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "agent-password")
	t.Setenv("RESTIC_REPOSITORY", "/elsewhere")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "agent-secret")
	f := newFixture(t, map[string]reply{"cat": catConfig})
	f.repo.EnvFile = "/etc/sard/main.env"
	f.files["/etc/sard/main.env"] = "# backend\nAWS_ACCESS_KEY_ID=AKIA1\n\nAWS_SECRET_ACCESS_KEY=repo=secret\n"
	if _, err := f.build().ID(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/var/cache/sard/restic",
		"RESTIC_CACHE_DIR=/var/cache/sard/restic",
		"RESTIC_REPOSITORY=/srv/restic/main",
		"RESTIC_PASSWORD_FILE=/etc/sard/main.pass",
		"AWS_ACCESS_KEY_ID=AKIA1",
		"AWS_SECRET_ACCESS_KEY=repo=secret",
	}
	if got := f.exec.call("cat").Env; !slices.Equal(got, want) {
		t.Fatalf("env = %q\nwant  %q", got, want)
	}
}

func TestEnvironmentDefaults(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	cli := restic.New(restic.Options{Exec: f.exec, Keys: f.keys, ReadFile: f.files.read}, f.repo)
	if _, err := cli.ID(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.exec.call("cat")
	if got.Path != "restic" || !slices.Contains(got.Env, "PATH="+restic.DefaultPath) ||
		!slices.Contains(got.Env, "RESTIC_CACHE_DIR="+restic.DefaultCacheDir) {
		t.Fatalf("path = %q, env = %q", got.Path, got.Env)
	}
}

// Variables that change how restic finds its repository, key or code are
// refused in env_file; the error names the variable, never a value.
func TestEnvFileRejectsDangerousVariables(t *testing.T) {
	forbidden := []string{
		"RESTIC_PASSWORD", "RESTIC_PASSWORD_COMMAND", "RESTIC_REPOSITORY", "PATH", "HOME",
		"XDG_CACHE_HOME", "TMPDIR", "LD_PRELOAD", "LD_LIBRARY_PATH", "GODEBUG", "GOTRACEBACK",
		"DEBUG_LOG", "TERM", "SSH_AUTH_SOCK",
	}
	for _, name := range forbidden {
		f := newFixture(t, map[string]reply{"cat": catConfig})
		f.repo.EnvFile = "/etc/sard/main.env"
		f.files["/etc/sard/main.env"] = "AWS_ACCESS_KEY_ID=AKIA1\n" + name + "=s3cr3t-value\n"
		_, err := f.build().ID(context.Background())
		want := `env_file "/etc/sard/main.env": line 2: ` + name + " is not allowed"
		if !errors.Is(err, restic.ErrInvalidEnvFile) || !strings.HasSuffix(err.Error(), want) || len(f.exec.calls) != 0 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(f.exec.calls))
		}
	}
}

func TestEnvFileAllowsBackendVariables(t *testing.T) {
	for _, name := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "B2_ACCOUNT_KEY", "HTTPS_PROXY", "AZURE_ACCOUNT_KEY", "_X1"} {
		f := newFixture(t, map[string]reply{"cat": catConfig})
		f.repo.EnvFile = "/e"
		f.files["/e"] = name + "=v\n"
		if _, err := f.build().ID(context.Background()); err != nil || !slices.Contains(f.exec.call("cat").Env, name+"=v") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestEnvFileRejectsMalformedLinesWithoutEchoingThem(t *testing.T) {
	cases := map[string]string{
		"s3cr3t-value\n":                   "line 1: want KEY=VALUE",
		"# c\nexport AWS_KEY=s3cr3t-value": "line 2: want KEY=VALUE",
		"=s3cr3t-value\n":                  "line 1: want KEY=VALUE",
		"AWS KEY=s3cr3t-value\n":           "line 1: want KEY=VALUE",
		"1AWS=s3cr3t-value\n":              "line 1: want KEY=VALUE",
		"AWS-KEY=s3cr3t-value\n":           "line 1: want KEY=VALUE",
	}
	for content, want := range cases {
		f := newFixture(t, map[string]reply{"cat": catConfig})
		f.repo.EnvFile = "/e"
		f.files["/e"] = content
		_, err := f.build().ID(context.Background())
		if !errors.Is(err, restic.ErrInvalidEnvFile) || !strings.HasSuffix(err.Error(), want) || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%q: err = %v", content, err)
		}
	}
}

func TestUnreadableEnvFileIsAnError(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	f.repo.EnvFile = "/missing.env"
	if _, err := f.build().ID(context.Background()); !errors.Is(err, fs.ErrNotExist) || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestKeyProviderFailureStopsBeforeRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	f.keys.err = crypto.ErrUnknownRepository
	if _, err := f.build().ID(context.Background()); !errors.Is(err, crypto.ErrUnknownRepository) || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelledContextWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFixture(t, map[string]reply{"cat": {stderr: "backup-sigterm.stderr", code: 130, during: cancel}})
	if _, err := f.build().ID(ctx); !errors.Is(err, context.Canceled) || err.Error() != "restic cat: context canceled" {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreWritesTheSnapshotIntoTheTarget(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {stdout: "restore.stdout"}})
	if err := f.build().Restore(context.Background(), "6c719cbc", "/srv/restore"); err != nil {
		t.Fatal(err)
	}
	want := []string{"restore", "--json", "--target", "/srv/restore", "--", "6c719cbc"}
	if got := f.exec.call("restore"); !slices.Equal(got.Args, want) || !slices.Contains(got.Env, "RESTIC_PASSWORD_FILE=/etc/sard/main.pass") {
		t.Fatalf("args = %q, env = %q", got.Args, got.Env)
	}
}

func TestRestoreOfAMissingSnapshot(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {stderr: "restore-missing.stderr", code: 1}})
	err := f.build().Restore(context.Background(), "deadbeef", "/srv/restore")
	want := `restic restore: exit code 1: Fatal: failed to find snapshot: no matching ID found for prefix "deadbeef"`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreRejectsInvalidRequests(t *testing.T) {
	for _, c := range [][2]string{{"", "/srv/restore"}, {"6c719cbc", ""}} {
		f := newFixture(t, nil)
		if err := f.build().Restore(context.Background(), c[0], c[1]); !errors.Is(err, restic.ErrInvalidRequest) || len(f.exec.calls) != 0 {
			t.Errorf("%q: err = %v", c, err)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
