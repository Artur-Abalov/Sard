// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// docs/specs/agent/repo-init.feature: the secrets that must never be printed.
const (
	passMarker = "PASS-MARKER"
	envMarker  = "ENV-MARKER"
	urlMarker  = "URL-MARKER"
)

const goldenID = "0fa4b1261960f1b5334694f4603c60dd52a5c2b2c8f262aa6c31a7fd5b11e406"

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fakeRepo is the state of one repository behind the fake restic.
type fakeRepo struct {
	id            string
	initialized   bool
	wrongPassword bool   // cat config: the password does not open it
	fatal         string // every repository command fails with this message
	hangCat       bool   // cat config never finishes
	hangInit      bool   // init never finishes
	raceExists    bool   // cat config: none; init: the config file already exists
	initNoID      bool   // init succeeds but prints no id
	initEntered   chan struct{}
	terminated    bool
}

// fakeCall is one restic invocation.
type fakeCall struct {
	sub          string
	repository   string
	passwordFile string
	env          []string
	runAs        *restic.RunAs
}

// fakeRestic replays the golden output of restic 0.19.1 (as the A5a
// tests do) and records its calls.
type fakeRestic struct {
	t       *testing.T
	path    string // the only path that exists
	version string
	// hello: the binary answers "hello" to version.
	hello bool
	// versionFails: version exits with 1.
	versionFails bool

	mu    sync.Mutex
	repos map[string]*fakeRepo
	calls []fakeCall
}

func newFakeRestic(t *testing.T, path string) *fakeRestic {
	return &fakeRestic{t: t, path: path, version: "0.19.1", repos: map[string]*fakeRepo{}}
}

func (f *fakeRestic) repo(url string) *fakeRepo {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.repos[url]
	if !ok {
		sum := sha256.Sum256([]byte(url))
		r = &fakeRepo{id: hex.EncodeToString(sum[:]), initEntered: make(chan struct{})}
		f.repos[url] = r
	}
	return r
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

func (f *fakeRestic) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if cmd.Path != f.path {
		return -1, &fs.PathError{Op: "fork/exec", Path: cmd.Path, Err: syscall.ENOENT}
	}
	sub := cmd.Args[0]
	url := envValue(cmd.Env, "RESTIC_REPOSITORY")
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{sub: sub, repository: url, passwordFile: envValue(cmd.Env, "RESTIC_PASSWORD_FILE"), env: cmd.Env, runAs: cmd.RunAs})
	f.mu.Unlock()
	if sub == "version" {
		return f.printVersion(cmd)
	}
	return f.runRepoCommand(ctx, cmd, sub, f.repo(url))
}

func (f *fakeRestic) printVersion(cmd restic.Command) (int, error) {
	if f.versionFails {
		return 1, nil
	}
	if f.hello {
		cmd.Stdout([]byte("hello"))
		return 0, nil
	}
	cmd.Stdout([]byte("restic " + f.version + " compiled with go1.26.4 on linux/amd64"))
	return 0, nil
}

func lines(cb func([]byte), text string) {
	for l := range strings.Lines(text) {
		cb([]byte(strings.TrimSuffix(l, "\n")))
	}
}

func (f *fakeRestic) runRepoCommand(ctx context.Context, cmd restic.Command, sub string, r *fakeRepo) (int, error) {
	switch {
	case r.fatal != "":
		cmd.Stderr([]byte("Fatal: " + r.fatal))
		return 1, nil
	case sub == "cat":
		return f.cat(ctx, cmd, r)
	case sub == "init":
		return f.init(ctx, cmd, r)
	}
	f.t.Fatalf("unexpected restic %q", cmd.Args)
	return 0, nil
}

// hang blocks like a restic waiting for its backend, until SIGTERM.
func (f *fakeRestic) hang(ctx context.Context, r *fakeRepo) (int, error) {
	<-ctx.Done()
	f.mu.Lock()
	r.terminated = true
	f.mu.Unlock()
	return -1, nil
}

func (f *fakeRestic) cat(ctx context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	switch {
	case r.hangCat:
		return f.hang(ctx, r)
	case r.wrongPassword:
		lines(cmd.Stderr, golden(f.t, "wrong-password.stderr"))
		return 12, nil
	case !r.initialized || r.raceExists:
		lines(cmd.Stderr, golden(f.t, "no-repository.stderr"))
		return 10, nil
	}
	lines(cmd.Stdout, strings.ReplaceAll(golden(f.t, "cat-config.json"), goldenID, r.id))
	return 0, nil
}

func (f *fakeRestic) init(ctx context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	if r.hangInit {
		close(r.initEntered)
		return f.hang(ctx, r)
	}
	if r.initialized || r.raceExists {
		lines(cmd.Stderr, golden(f.t, "init-exists.stderr"))
		return 1, nil
	}
	if r.initNoID {
		cmd.Stdout([]byte(`{"message_type":"initialized"}`))
		return 0, nil
	}
	r.initialized = true
	lines(cmd.Stdout, strings.ReplaceAll(golden(f.t, "init.json"), goldenID, r.id))
	return 0, nil
}

func (f *fakeRestic) subs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var subs []string
	for _, c := range f.calls {
		subs = append(subs, c.sub)
	}
	return subs
}

// backendCalls counts the calls that reach a repository (everything but version).
func (f *fakeRestic) backendCalls() int {
	n := 0
	for _, s := range f.subs() {
		if s != "version" {
			n++
		}
	}
	return n
}

func (f *fakeRestic) callsTo(url, sub string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var got []fakeCall
	for _, c := range f.calls {
		if c.repository == url && c.sub == sub {
			got = append(got, c)
		}
	}
	return got
}

// repoHost is the world of one test: the files of the conventions section
// of the spec (P, P2, E, C) in a temporary directory, a fake restic and
// a clock the test drives.
type repoHost struct {
	t       *testing.T
	dir     string
	cfg     config.Config
	cfgPath string
	restic  *fakeRestic
	clock   *fakeEnrollClock
	deps    hostDeps
}

func (h *repoHost) path(name string) string { return filepath.Join(h.dir, name) }

func (h *repoHost) cacheDir() string { return h.path("cache") }
func (h *repoHost) pass() string     { return h.path("main.pass") }
func (h *repoHost) pass2() string    { return h.path("offsite.pass") }
func (h *repoHost) envFile() string  { return h.path("main.env") }
func (h *repoHost) repoURL() string  { return h.path("repo") }

const offsiteURL = "rest:http://qa:" + urlMarker + "@127.0.0.1:9/offsite"

func newRepoHost(t *testing.T) *repoHost {
	t.Helper()
	dir := t.TempDir()
	h := &repoHost{t: t, dir: dir, clock: newFakeEnrollClock()}
	h.restic = newFakeRestic(t, h.path("restic"))
	h.write(h.pass(), passMarker+"\n", 0o600)
	h.write(h.pass2(), "offsite-password\n", 0o600)
	h.write(h.envFile(), "AWS_SECRET_ACCESS_KEY="+envMarker+"\n", 0o600)
	h.cfg = config.Config{
		Server: config.Server{Address: "127.0.0.1:1"},
		TLS:    config.TLS{CAFile: h.path("tls/ca.pem"), CertFile: h.path("tls/agent.pem"), KeyFile: h.path("tls/agent.key")},
		Restic: config.Restic{Path: h.path("restic"), CacheDir: h.path("cache")},
		Repositories: []config.Repository{
			{Name: "main", URL: h.repoURL(), PasswordFile: h.pass(), EnvFile: h.envFile()},
			{Name: "offsite", URL: offsiteURL, PasswordFile: h.pass2()},
		},
	}
	if err := os.Mkdir(h.cacheDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	h.deps = productionHostDeps()
	useTestServiceUser(&h.deps)
	h.deps.defaultCacheDir = h.path("default-cache")
	h.deps.clock = h.clock
	h.deps.exec = h.restic
	h.deps.openLock = openAsNonRoot
	h.deps.executable = func() (string, error) { return h.path("bin/sard-agent"), nil }
	h.saveConfig()
	return h
}

// openAsNonRoot is os.OpenFile as the kernel answers a user other than root:
// creating a file in a directory without the owner write bit is EACCES. The
// tests may run as root, which ignores directory modes.
func openAsNonRoot(name string, flag int, perm os.FileMode) (*os.File, error) {
	if flag&os.O_CREATE != 0 {
		dir := filepath.Dir(name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() && info.Mode().Perm()&0o200 == 0 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
		}
	}
	return os.OpenFile(name, flag, perm)
}

func (h *repoHost) write(path, content string, mode os.FileMode) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		h.t.Fatal(err)
	}
}

// saveConfig writes h.cfg as the config C.
func (h *repoHost) saveConfig() {
	h.t.Helper()
	data, err := yaml.Marshal(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.cfgPath = h.path("agent.yaml")
	h.write(h.cfgPath, string(data), 0o600)
}

// main is the repository "main" of the config.
func (h *repoHost) main() *fakeRepo { return h.restic.repo(h.repoURL()) }

func (h *repoHost) offsite() *fakeRepo { return h.restic.repo(offsiteURL) }

// run is `sard-agent repo <args>`; "C" stands for the config path.
func (h *repoHost) run(args ...string) (code int, stdout, stderr string) {
	return h.runCtx(context.Background(), args...)
}

func (h *repoHost) runCtx(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = runRepoWithDeps(ctx, h.subst(args), &out, &errOut, h.deps)
	return code, out.String(), errOut.String()
}

func (h *repoHost) subst(args []string) []string {
	got := make([]string, len(args))
	for i, a := range args {
		if a == "C" {
			a = h.cfgPath
		}
		got[i] = a
	}
	return got
}

// initCmd is "the command": sard-agent repo init --config C main.
func (h *repoHost) initCmd(extra ...string) (int, string, string) {
	return h.run(append([]string{"init", "--config", "C"}, append(extra, "main")...)...)
}

func (h *repoHost) listCmd(extra ...string) (int, string, string) {
	return h.run(append([]string{"list", "--config", "C"}, extra...)...)
}

// snapshot describes every file under the host directory: "the host is unchanged".
func (h *repoHost) snapshot() map[string]string {
	h.t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(h.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			content = string(data)
		}
		uid := info.Sys().(*syscall.Stat_t).Uid
		got[p] = fmt.Sprintf("%v %d %q", info.Mode(), uid, content)
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

func (h *repoHost) assertUnchanged(before map[string]string) {
	h.t.Helper()
	h.assertUnchangedWithout(before)
}

func dropTrees(files map[string]string, roots []string) {
	for p := range files {
		for _, root := range roots {
			if p == root || strings.HasPrefix(p, root+"/") {
				delete(files, p)
			}
		}
	}
}

func (h *repoHost) assertUnchangedWithout(before map[string]string, skip ...string) {
	h.t.Helper()
	after := h.snapshot()
	dropTrees(after, skip)
	var diffs []string
	for p, v := range before {
		if after[p] != v {
			diffs = append(diffs, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			diffs = append(diffs, p+" (new)")
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		h.t.Fatalf("the host changed: %v", diffs)
	}
}

func (h *repoHost) assertNoBackendCalls() {
	h.t.Helper()
	if n := h.restic.backendCalls(); n != 0 {
		h.t.Fatalf("the backend was called %d times: %v", n, h.restic.subs())
	}
}

// assertNoSecrets is "Секреты не раскрыты".
func assertNoSecrets(t *testing.T, outputs ...string) {
	t.Helper()
	for _, out := range outputs {
		for _, marker := range []string{passMarker, envMarker, urlMarker} {
			if strings.Contains(out, marker) {
				t.Fatalf("%s is in the output:\n%s", marker, out)
			}
		}
	}
}

func assertReason(t *testing.T, stderr, reason string) {
	t.Helper()
	if !strings.Contains(stderr, reason) {
		t.Fatalf("stderr does not name %s:\n%s", reason, stderr)
	}
}

func assertCode(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("exit code = %d, want %d", got, want)
	}
}

var hexID = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

func yield() { time.Sleep(time.Millisecond) }
