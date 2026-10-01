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

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "Агент без пригодного restic не стартует" (@start). The restic here
// is a real script or file, run by the real process executor.

// resticScript writes an executable that answers `restic version` like the
// release named version.
func resticScript(t *testing.T, path, version string) string {
	t.Helper()
	body := "#!/bin/sh\necho 'restic " + version + " compiled with go1.26.4 on linux/amd64'\n"
	writeFile(t, path, []byte(body), 0o755)
	return path
}

// startHost is an agent host: a valid identity, a counting server and a
// config whose restic section the test chooses.
type startHost struct {
	t        *testing.T
	dir      string
	accepted func() int32
	address  string
}

func newStartHost(t *testing.T) *startHost {
	t.Helper()
	dir := t.TempDir()
	writeIdentity(t, dir)
	addr, accepted := countingServer(t)
	return &startHost{t: t, dir: dir, address: addr, accepted: accepted.Load}
}

// config is the agent config with the given extra YAML (restic, repositories).
func (h *startHost) config(extra string) string {
	return writeConfig(h.t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\n"+extra)
}

// runFor starts the agent and stops it after d, as a service manager would.
func (h *startHost) runFor(d time.Duration, args ...string) (code int, stdout, stderr string) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var out, errOut bytes.Buffer
	code = run(ctx, args, &out, &errOut, fixedHostname)
	return code, out.String(), errOut.String()
}

func (h *startHost) refuses(cfg string) (stdout, stderr string) {
	h.t.Helper()
	code, stdout, stderr := h.runFor(2*time.Second, "--config", cfg)
	if code != 1 {
		h.t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "connecting to") || h.accepted() != 0 {
		h.t.Errorf("stdout = %q, connections = %d", stdout, h.accepted())
	}
	return stdout, stderr
}

func (h *startHost) connects(cfg string) string {
	h.t.Helper()
	code, _, stderr := h.runFor(400*time.Millisecond, "--config", cfg)
	if code != 0 || h.accepted() == 0 {
		h.t.Fatalf("exit code = %d, connections = %d, stderr = %q", code, h.accepted(), stderr)
	}
	return stderr
}

// restic не найден по пути из конфига — агент не стартует
func TestTheAgentDoesNotStartWhenResticIsNotFoundAtTheConfiguredPath(t *testing.T) {
	h := newStartHost(t)
	q := filepath.Join(h.dir, "nowhere", "restic")
	_, stderr := h.refuses(h.config("restic: {path: " + q + "}\n"))
	if !strings.HasPrefix(stderr, "sard-agent: ") {
		t.Errorf("stderr = %q", stderr)
	}
	for _, want := range []string{"RESTIC_NOT_FOUND", "restic.path", q, restic.Minimum.String()} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr)
		}
	}
}

// restic не найден рядом с агентом — агент не стартует и называет путь
func TestTheAgentDoesNotStartWhenResticIsNotNextToIt(t *testing.T) {
	h := newStartHost(t)
	exe := func() (string, error) { return filepath.Join(h.dir, "bin", "sard-agent"), nil }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := start(ctx, h.config(""), &bytes.Buffer{}, fixedHostname, exe)
	if err == nil {
		t.Fatal("the agent started")
	}
	for _, want := range []string{"RESTIC_NOT_FOUND", filepath.Join(h.dir, "bin", "restic"), "restic.path is not set", "in the agent config"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q: %v", want, err)
		}
	}
	if h.accepted() != 0 {
		t.Error("the agent connected")
	}
}

// Путь рядом с агентом берётся по настоящему файлу, а не по символьной ссылке
func TestTheResticNextToTheAgentIsFoundByTheRealFileNotTheSymlink(t *testing.T) {
	h := newStartHost(t)
	real := filepath.Join(h.dir, "L")
	link := filepath.Join(h.dir, "M")
	for _, d := range []string{real, link} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(real, "sard-agent"), []byte("x"), 0o755)
	resticScript(t, filepath.Join(real, "restic"), "0.19.1")
	if err := os.Symlink(filepath.Join(real, "sard-agent"), filepath.Join(link, "sard-agent")); err != nil {
		t.Fatal(err)
	}
	exe := func() (string, error) { return filepath.Join(link, "sard-agent"), nil }
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := start(ctx, h.config(""), &bytes.Buffer{}, fixedHostname, exe); err != nil {
		t.Fatalf("start: %v", err)
	}
	if h.accepted() == 0 {
		t.Fatal("the agent did not connect")
	}
}

// Устаревший restic — агент не стартует и называет версии
func TestTheAgentDoesNotStartWithAnOutdatedRestic(t *testing.T) {
	for _, version := range []string{"0.18.1", "0.9.6"} {
		h := newStartHost(t)
		path := resticScript(t, filepath.Join(h.dir, "restic"), version)
		_, stderr := h.refuses(h.config("restic: {path: " + path + "}\n"))
		for _, want := range []string{"RESTIC_TOO_OLD", version, restic.Minimum.String()} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr does not contain %q: %s", version, want, stderr)
			}
		}
	}
}

// Непригодный restic — агент не стартует
func TestTheAgentDoesNotStartWithAnUnusableRestic(t *testing.T) {
	h := newStartHost(t)
	dir := filepath.Join(h.dir, "a-directory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(h.dir, "plain")
	writeFile(t, plain, []byte("#!/bin/sh\n"), 0o644)
	hello := filepath.Join(h.dir, "hello")
	writeFile(t, hello, []byte("#!/bin/sh\necho hello\n"), 0o755)
	for _, path := range []string{dir, plain, hello} {
		_, stderr := h.refuses(h.config("restic: {path: " + path + "}\n"))
		if !strings.Contains(stderr, "RESTIC_UNUSABLE") || !strings.Contains(stderr, path) {
			t.Errorf("%s: stderr = %q", path, stderr)
		}
	}
}

// restic минимальной версии и новее принимается
func TestResticOfTheMinimumVersionAndNewerIsAccepted(t *testing.T) {
	for _, version := range []string{"0.19.0", "0.19.1", "0.19.0-dev", "0.20.0", "1.0.0"} {
		h := newStartHost(t)
		path := resticScript(t, filepath.Join(h.dir, "restic"), version)
		if stderr := h.connects(h.config("restic: {path: " + path + "}\n")); strings.Contains(stderr, "RESTIC_TOO_OLD") {
			t.Errorf("%s: stderr = %q", version, stderr)
		}
	}
}

// Агент без репозиториев тоже требует restic
func TestAnAgentWithoutRepositoriesStillNeedsRestic(t *testing.T) {
	h := newStartHost(t)
	h.refuses(h.config("restic: {path: " + filepath.Join(h.dir, "nowhere") + "}\nrepositories: []\n"))
}

// Нарушение прав секретного файла сообщается раньше проблемы с restic
func TestASecretFilePermissionProblemIsReportedBeforeAResticProblem(t *testing.T) {
	h := newStartHost(t)
	pass := filepath.Join(h.dir, "main.pass")
	writeFile(t, pass, []byte("x\n"), 0o644)
	cfg := h.config("restic: {path: " + filepath.Join(h.dir, "nowhere") + "}\n" +
		"repositories:\n  - {name: main, url: " + filepath.Join(h.dir, "repo") + ", password_file: " + pass + "}\n")
	_, stderr := h.refuses(cfg)
	if !strings.Contains(stderr, pass) || !strings.Contains(stderr, "secret file") || strings.Contains(stderr, "RESTIC_NOT_FOUND") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Файл пароля, созданный командой, проходит проверку при старте агента
func TestThePasswordFileCreatedByTheCommandPassesTheStartOfTheAgent(t *testing.T) {
	h := newStartHost(t)
	script := resticScript(t, filepath.Join(h.dir, "restic"), "0.19.1")
	pass := filepath.Join(h.dir, "main.pass")
	cfgPath := h.config("restic: {path: " + script + "}\n" +
		"repositories:\n  - {name: main, url: " + filepath.Join(h.dir, "repo") + ", password_file: " + pass + "}\n")
	// The command itself, with a fake restic behind the real script's path.
	r := newRepoHost(t)
	r.restic.path = script
	r.deps.exec = r.restic
	r.deps.defaultCacheDir = r.cacheDir() // the config leaves restic.cache_dir unset
	var out, errOut bytes.Buffer
	code := runRepoWithDeps(context.Background(), []string{"init", "--config", cfgPath, "--generate-password", "main"}, &out, &errOut, r.deps)
	if code != exitOK {
		t.Fatalf("repo init: code = %d, stderr = %q", code, errOut.String())
	}
	h.connects(cfgPath)
}

// Регистрация агента не требует restic
func TestEnrollDoesNotNeedRestic(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	data, err := os.ReadFile(f.h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := append(data, []byte("restic: {path: /nonexistent/restic}\n")...)
	if err := os.WriteFile(f.h.configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d; stdout=%q stderr=%q", code, out, errOut)
	}
}
