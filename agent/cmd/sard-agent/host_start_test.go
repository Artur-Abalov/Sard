// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Rule "Конфиг агента собирается из основного файла и фрагментов каталога
// agent-d" (@start): the agent started with the main config C.

// unavailable is a server that never lets the agent register.
func unavailable(context.Context) error { return status.Error(codes.Unavailable, "no") }

// fragmentHost is a refusalHost with fragments next to its config.
type fragmentHost struct {
	*refusalHost
	fragments string
}

func newFragmentHost(t *testing.T, extraMain string) *fragmentHost {
	t.Helper()
	h := newRefusalHost(t, unavailable)
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\n"+h.restic+extraMain)
	return &fragmentHost{refusalHost: h, fragments: filepath.Join(filepath.Dir(h.cfg), "agent.d")}
}

func (h *fragmentHost) fragment(name, body string) string {
	h.t.Helper()
	path := filepath.Join(h.fragments, name)
	ok(h.t, os.MkdirAll(h.fragments, 0o750))
	writeFile(h.t, path, []byte(body), 0o640)
	return path
}

func (h *fragmentHost) registered() (repos, secretNames []string) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for h.server.last.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	req := h.server.last.Load()
	if req == nil {
		h.t.Fatal("the agent did not register")
	}
	for _, r := range req.GetRepositories() {
		repos = append(repos, r.GetName())
	}
	return repos, req.GetSecretNames()
}

func TestTheAgentRegistersTheRepositoryOfAFragment(t *testing.T) {
	h := newFragmentHost(t, "repositories:\n  - {name: base, url: /srv/base, password_file: "+filepath.Join(t.TempDir(), "base.pass")+"}\n")
	pass := filepath.Join(t.TempDir(), "extra.pass")
	writeFile(t, pass, []byte("x\n"), 0o600)
	h.fragment("repo-extra.yaml", "repositories:\n  - {name: extra, url: /srv/extra, password_file: "+pass+"}\n")
	h.run(time.Second)
	repos, _ := h.registered()
	if strings.Join(repos, ",") != "base,extra" {
		t.Fatalf("repositories %v", repos)
	}
}

func TestTheAgentRegistersTheSecretOfAFragment(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "pg")
	writeFile(t, secret, []byte("v"), 0o600)
	h := newFragmentHost(t, "secrets: {pg: "+secret+"}\n")
	db := filepath.Join(t.TempDir(), "db")
	writeFile(t, db, []byte("v"), 0o600)
	h.fragment("secret-db.yaml", "secrets:\n  db: "+db+"\n")
	h.run(time.Second)
	_, names := h.registered()
	if strings.Join(names, ",") != "db,pg" {
		t.Fatalf("secret names %v", names)
	}
}

func TestADuplicateNameInAFragmentStopsTheAgentBeforeConnecting(t *testing.T) {
	h := newStartHost(t)
	pass := filepath.Join(h.dir, "base.pass")
	writeFile(t, pass, []byte("x\n"), 0o600)
	cfg := h.config("repositories:\n  - {name: base, url: /srv/base, password_file: " + pass + "}\n")
	fragment := filepath.Join(filepath.Dir(cfg), "agent.d", "repo-base.yaml")
	ok(t, os.MkdirAll(filepath.Dir(fragment), 0o750))
	writeFile(t, fragment, []byte("repositories:\n  - {name: base, url: /srv/other, password_file: "+pass+"}\n"), 0o640)
	_, stderr := h.refuses(cfg)
	for _, want := range []string{"DUPLICATE_NAME", "base", cfg, fragment} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}

func TestAPasswordFileOfAFragmentThatIsOpenToTheGroupNamesTheFragment(t *testing.T) {
	h := newStartHost(t)
	pass := filepath.Join(h.dir, "extra.pass")
	writeFile(t, pass, []byte("x\n"), 0o640)
	cfg := h.config("")
	fragment := filepath.Join(filepath.Dir(cfg), "agent.d", "repo-extra.yaml")
	ok(t, os.MkdirAll(filepath.Dir(fragment), 0o750))
	writeFile(t, fragment, []byte("repositories:\n  - {name: extra, url: /srv/extra, password_file: "+pass+"}\n"), 0o640)
	_, stderr := h.refuses(cfg)
	for _, want := range []string{fragment, "password_file", pass} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}

// hostDepsFor is the commands run by root for the config at cfg, the
// service user being the user of this test (so the files the commands
// make pass A1 when the agent starts); owner changes are only recorded.
func hostDepsFor(t *testing.T) (hostDeps, *fakeFS) {
	t.Helper()
	deps := productionHostDeps()
	useTestServiceUser(&deps)
	fsys := newFakeFS()
	deps.euid = 0
	deps.fs = fsys
	deps.systemd = &fakeSystemd{}
	deps.openLock = openAsNonRoot
	deps.defaultCacheDir = t.TempDir()
	return deps, fsys
}

func TestASecretMadeUnderSudoPassesTheCheckOfTheServiceStart(t *testing.T) {
	h := newStartHost(t)
	cfg := h.config(withRestic(t))
	deps, _ := hostDepsFor(t)
	deps.stdin = strings.NewReader("SECRET-MARKER")
	var out, errOut bytes.Buffer
	code := runSecretWithDeps(context.Background(), []string{"set", "db", "--stdin", "--config", cfg}, &out, &errOut, deps)
	assertCode(t, code, exitOK)
	h.connects(cfg)
}

func TestAnIdentityWrittenUnderSudoPassesTheCheckOfTheServiceStart(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := f.enrollWith(rootEnrollDeps(newFakeFS()))
	assertCode(t, code, exitOK)
	cfg, err := config.Load(f.h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.CheckAll(cfg, uint32(os.Getuid()), secrets.RealStat); err != nil {
		t.Fatalf("A1: %v (stderr %q)", err, errOut)
	}
}

// resticAnswering is a script that is restic version 0.19.1 and says id for
// the repository config.
func resticAnswering(t *testing.T, id string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restic")
	body := "#!/bin/sh\ncase \"$1\" in\n  version) echo 'restic " + restic.Pinned.String() + " compiled with go1.26.4 on linux/amd64';;\n  cat) echo '{\"version\":2,\"id\":\"" + id + "\"}';;\nesac\n"
	writeFile(t, path, []byte(body), 0o755)
	return path
}

func TestAfterRepoAddTheAgentTellsTheServerTheRepositoryID(t *testing.T) {
	h := newRefusalHost(t, unavailable)
	id := strings.Repeat("ab", 32)
	script := resticAnswering(t, id)
	// The config of the agent: the one of the start host, with the script as restic.
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\nrestic: {path: "+script+", cache_dir: "+t.TempDir()+"}\n")
	deps, _ := hostDepsFor(t)
	deps.exec = keepsTheCaller{t: t, want: restic.RunAs{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}}
	deps.executable = func() (string, error) { return script, nil }
	repoDir := filepath.Join(t.TempDir(), "repo-extra")
	var out, errOut bytes.Buffer
	code := runRepoWithDeps(context.Background(), []string{"add", "extra", repoDir, "--config", h.cfg}, &out, &errOut, deps)
	assertCode(t, code, exitOK)
	if !strings.Contains(out.String(), id) {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	h.run(time.Second)
	req := h.server.last.Load()
	if req == nil || len(req.GetRepositories()) != 1 || req.GetRepositories()[0].GetName() != "extra" || req.GetRepositories()[0].GetRepositoryId() != id {
		t.Fatalf("register request %v", req)
	}
}

// A8b: the agent started after "repo add" of an s3: address.
func TestAfterRepoAddOfS3TheAgentTellsTheServerTheBackendAndTheID(t *testing.T) {
	h := newRefusalHost(t, unavailable)
	id := strings.Repeat("cd", 32)
	script := resticAnswering(t, id)
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\nrestic: {path: "+script+", cache_dir: "+t.TempDir()+"}\n")
	deps, _ := hostDepsFor(t)
	deps.exec = keepsTheCaller{t: t, want: restic.RunAs{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}}
	deps.executable = func() (string, error) { return script, nil }
	deps.stdin = strings.NewReader(s3Marker)
	password := filepath.Join(t.TempDir(), "password")
	writeFile(t, password, []byte("PASS\n"), 0o600)
	var out, errOut bytes.Buffer
	code := runRepoWithDeps(context.Background(), []string{"add", "extra", s3Address, "--access-key-id", keyID1,
		"--secret-key-stdin", "--password-from-file", password, "--config", h.cfg}, &out, &errOut, deps)
	assertCode(t, code, exitOK)
	cfg, err := config.Load(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.CheckAll(cfg, uint32(os.Getuid()), secrets.RealStat); err != nil {
		t.Fatalf("A1: %v (stderr %q)", err, errOut.String())
	}
	h.run(time.Second)
	req := h.server.last.Load()
	if req == nil || len(req.GetRepositories()) != 1 {
		t.Fatalf("register request %v", req)
	}
	if r := req.GetRepositories()[0]; r.GetName() != "extra" || r.GetBackend() != "s3" || r.GetRepositoryId() != id {
		t.Fatalf("repository %v", r)
	}
}

// sftpDepsFor are the deps of a command run for the config at cfg, with
// the service user the user of this test, a home for it, and the client.
func sftpDepsFor(t *testing.T, script string) hostDeps {
	t.Helper()
	deps, fsys := hostDepsFor(t)
	deps.exec = keepsTheCaller{t: t, want: restic.RunAs{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}}
	deps.executable = func() (string, error) { return script, nil }
	fsys.defaultUID = uint32(os.Getuid())
	self := hostsetup.User{Name: "sard-agent", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Home: t.TempDir()}
	deps.lookupUser = users(self)
	programs := t.TempDir()
	for _, program := range []string{"ssh", "sftp", "ssh-keygen", "ssh-keyscan"} {
		writeFile(t, filepath.Join(programs, program), []byte("#!/bin/sh\n"), 0o755)
	}
	deps.pathEnv = programs
	deps.sshExec = &fakeSSH{t: t, fsys: fsys, srv: &sshServer{keys: []serverKey{keyED}, authorized: true, hang: map[string]bool{}, hostname: "host1"}}
	deps.hostname = func() (string, error) { return "host1", nil }
	return deps
}

// A8b-2: the agent started after "repo add" of an sftp: address.
func TestAfterRepoAddOfSFTPTheAgentTellsTheServerTheBackendAndTheID(t *testing.T) {
	h := newRefusalHost(t, unavailable)
	id := strings.Repeat("ef", 32)
	script := resticAnswering(t, id)
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\nrestic: {path: "+script+", cache_dir: "+t.TempDir()+"}\n")
	var out, errOut bytes.Buffer
	code := runRepoWithDeps(context.Background(), []string{"add", "extra", sftpAddress, "--host-key-fingerprint", keyED.fingerprint(), "--config", h.cfg}, &out, &errOut, sftpDepsFor(t, script))
	if code != exitOK || !strings.Contains(out.String(), id) {
		t.Fatalf("exit code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	h.run(time.Second)
	req := h.server.last.Load()
	if req == nil || len(req.GetRepositories()) != 1 {
		t.Fatalf("register request %v", req)
	}
	if r := req.GetRepositories()[0]; r.GetName() != "extra" || r.GetBackend() != "sftp" || r.GetRepositoryId() != id {
		t.Fatalf("repository %v", r)
	}
}
