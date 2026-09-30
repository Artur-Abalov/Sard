// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/repo-init.feature, rule "Успешная инициализация создаёт
// репозиторий и говорит, что делать дальше". Test names quote the scenarios.

// Итог успеха называет имя, тип бэкенда и repository_id
func TestSuccessSummaryNamesTheRepositoryTheBackendAndTheRepositoryID(t *testing.T) {
	h := newRepoHost(t)
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "main") || !strings.Contains(stdout, "local") || !strings.Contains(stdout, h.main().id) {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
	if !hexID.MatchString(stdout) {
		t.Fatalf("no 64-character hex repository_id in %q", stdout)
	}
	if !h.main().initialized {
		t.Fatal("restic init was not run")
	}
}

// Итог успеха предупреждает, что ключ есть только на этом хосте
func TestSuccessSummaryWarnsThatTheKeyIsOnlyOnThisHost(t *testing.T) {
	h := newRepoHost(t)
	_, stdout, _ := h.initCmd()
	for _, want := range []string{h.pass(), "outside this host", "only on this host", "unrecoverable"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

// Итог успеха говорит перезапустить службу агента
func TestSuccessSummaryTellsToRestartTheAgentService(t *testing.T) {
	h := newRepoHost(t)
	_, stdout, _ := h.initCmd()
	for _, want := range []string{"restart", "sard-agent", "repository_id"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

// Предупреждение о ключе печатается и когда вывод не терминал
func TestTheKeyWarningIsPrintedWhenStdoutIsAFile(t *testing.T) {
	h := newRepoHost(t)
	out, err := os.Create(h.path("out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	code := runRepoWithDeps(t.Context(), []string{"init", "--config", h.cfgPath, "main"}, out, os.Stderr, h.deps)
	assertCode(t, code, exitOK)
	data, err := os.ReadFile(out.Name())
	if err != nil || !strings.Contains(string(data), "only on this host") || !strings.Contains(string(data), "unrecoverable") {
		t.Fatalf("file = %q, err = %v", data, err)
	}
}

// Тип бэкенда в итоге берётся из адреса репозитория
func TestTheBackendTypeComesFromTheRepositoryAddress(t *testing.T) {
	for url, kind := range map[string]string{
		"/srv/backup/main":                   "local",
		"s3:https://s3.example.com/b/main":   "s3",
		"sftp:backup@nas.example.com:/main":  "sftp",
		"rest:https://rest.example.com/main": "rest",
	} {
		h := newRepoHost(t)
		h.cfg.Repositories[0].URL = url
		h.saveConfig()
		code, stdout, stderr := h.initCmd()
		assertCode(t, code, exitOK)
		if !regexp.MustCompile(`backend:\s+` + kind + `\n`).MatchString(stdout) {
			t.Errorf("%s: stdout = %q, stderr = %q", url, stdout, stderr)
		}
	}
}

// Команда не меняет конфиг и файл пароля
func TestTheCommandChangesNeitherTheConfigNorThePasswordFile(t *testing.T) {
	h := newRepoHost(t)
	before := h.snapshot()
	code, _, _ := h.initCmd()
	assertCode(t, code, exitOK)
	h.assertUnchanged(before)
}

// Команда не обращается к серверу Sard
func TestTheCommandDoesNotConnectToTheSardServer(t *testing.T) {
	h := newRepoHost(t)
	addr, accepted := countingServer(t)
	h.cfg.Server.Address = addr
	h.saveConfig()
	code, _, _ := h.initCmd()
	assertCode(t, code, exitOK)
	time.Sleep(50 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("%d connections to server.address", n)
	}
}

// Команда не читает стандартный ввод
func TestTheCommandDoesNotReadStandardInput(t *testing.T) {
	h := newRepoHost(t)
	// A pipe that never closes.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(); _ = r.Close() }()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	done := make(chan int, 1)
	go func() { code, _, _ := h.initCmd(); done <- code }()
	select {
	case code := <-done:
		assertCode(t, code, exitOK)
	case <-time.After(5 * time.Second):
		t.Fatal("the command waits for standard input")
	}
}

// Встроенный провайдер шифрования, заданный явно, принимается
func TestTheBuiltInCryptoProviderNamedExplicitlyIsAccepted(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].CryptoProvider = "restic-aes"
	h.saveConfig()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Переменные restic из окружения оператора не влияют на репозиторий
func TestResticVariablesOfTheOperatorEnvironmentDoNotReachRestic(t *testing.T) {
	h := newRepoHost(t)
	t.Setenv("RESTIC_REPOSITORY", "/somewhere/else")
	t.Setenv("RESTIC_PASSWORD", passMarker)
	code, _, _ := h.initCmd()
	assertCode(t, code, exitOK)
	for _, c := range h.restic.callsTo(h.repoURL(), "init") {
		for _, kv := range c.env {
			if strings.HasPrefix(kv, "RESTIC_PASSWORD=") || kv == "RESTIC_REPOSITORY=/somewhere/else" {
				t.Errorf("restic got %s", kv)
			}
		}
	}
	if len(h.restic.callsTo(h.repoURL(), "init")) != 1 {
		t.Fatal("restic init did not run on the repository of the config")
	}
}

// Второй репозиторий конфига инициализируется по своему имени
func TestTheSecondRepositoryOfTheConfigIsInitialisedByItsName(t *testing.T) {
	h := newRepoHost(t)
	code, stdout, stderr := h.run("init", "--config", "C", "offsite")
	assertCode(t, code, exitOK)
	calls := h.restic.callsTo(offsiteURL, "init")
	if len(calls) != 1 || calls[0].passwordFile != h.pass2() {
		t.Fatalf("calls = %+v, stderr = %q", calls, stderr)
	}
	if !strings.Contains(stdout, "rest") {
		t.Errorf("stdout = %q", stdout)
	}
}
