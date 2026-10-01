// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Существующий репозиторий не инициализируется повторно".

// Уже инициализированный репозиторий — отдельный отказ с его id
func TestAnInitialisedRepositoryIsRefusedWithItsID(t *testing.T) {
	h := newRepoHost(t)
	h.main().initialized = true
	before := h.snapshot()
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitIdentityExists)
	assertReason(t, stderr, "REPOSITORY_EXISTS")
	if !strings.Contains(stderr, h.main().id) {
		t.Errorf("stderr does not name the id: %s", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}
	if n := len(h.restic.callsTo(h.repoURL(), "init")); n != 0 {
		t.Errorf("restic init was called %d times: the data could be touched", n)
	}
	h.assertUnchanged(before)
}

// Повторный запуск после успеха сообщает тот же id
func TestARepeatAfterSuccessReportsTheSameID(t *testing.T) {
	h := newRepoHost(t)
	_, stdout, _ := h.initCmd()
	id := hexID.FindString(stdout)
	if id == "" {
		t.Fatalf("no id in %q", stdout)
	}
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitIdentityExists)
	if !strings.Contains(stderr, id) {
		t.Errorf("stderr = %q, want id %s", stderr, id)
	}
}

// Репозиторий, созданный между проверкой и инициализацией, не перезаписывается
func TestARepositoryCreatedBetweenTheCheckAndTheInitIsNotOverwritten(t *testing.T) {
	h := newRepoHost(t)
	h.main().raceExists = true
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitIdentityExists)
	assertReason(t, stderr, "REPOSITORY_EXISTS")
	if strings.Contains(stdout, "repository_id") || strings.Contains(stdout, "Initialized") {
		t.Errorf("stdout = %q", stdout)
	}
}

// Репозиторий по адресу есть, но файл пароля его не открывает
func TestARepositoryThatThePasswordFileDoesNotOpenIsAUsageError(t *testing.T) {
	h := newRepoHost(t)
	h.main().wrongPassword = true
	before := h.snapshot()
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "WRONG_PASSWORD")
	if !strings.Contains(stderr, `a repository already exists at the address of "main"`) || !strings.Contains(stderr, h.pass()+" does not open it") {
		t.Errorf("stderr = %q", stderr)
	}
	if n := len(h.restic.callsTo(h.repoURL(), "init")); n != 0 {
		t.Errorf("restic init was called %d times", n)
	}
	assertNoSecrets(t, stdout, stderr)
	h.assertUnchanged(before)
}

// Rule "Отказ бэкенда объясняется без учётных данных".

// Сетевая недоступность бэкенда — временная ошибка
func TestAnUnreachableBackendIsTemporary(t *testing.T) {
	for _, cause := range []string{"connection refused", "no such host", "i/o timeout", "network is unreachable"} {
		h := newRepoHost(t)
		h.cfg.Repositories[0].URL = "sftp:backup@nas.example.com:/main"
		h.saveConfig()
		h.restic.repo("sftp:backup@nas.example.com:/main").fatal = "dial tcp 10.0.0.1:22: " + cause
		code, stdout, stderr := h.initCmd()
		assertCode(t, code, exitTemporary)
		assertReason(t, stderr, "BACKEND_UNAVAILABLE")
		if !strings.Contains(stderr, "sftp") || !strings.Contains(stderr, cause) || !strings.Contains(stderr, "can be repeated") {
			t.Errorf("%s: stderr = %q", cause, stderr)
		}
		if strings.Contains(stdout, "repository_id") {
			t.Errorf("%s: stdout = %q", cause, stdout)
		}
	}
}

// Несетевой отказ бэкенда — ошибка агента с причиной от restic
func TestANonNetworkRefusalOfTheBackendIsAnAgentError(t *testing.T) {
	for _, cause := range []string{
		"The AWS Access Key Id you provided does not exist",
		"Access Denied",
		"x509 certificate signed by unknown authority",
		"permission denied",
	} {
		h := newRepoHost(t)
		h.cfg.Repositories[0].URL = "s3:https://s3.example.com/b/main"
		h.saveConfig()
		h.restic.repo("s3:https://s3.example.com/b/main").fatal = cause
		code, stdout, stderr := h.initCmd()
		assertCode(t, code, exitAgentError)
		assertReason(t, stderr, "BACKEND_REFUSED")
		if !strings.Contains(stderr, "s3") || !strings.Contains(stderr, cause) {
			t.Errorf("%s: stderr = %q", cause, stderr)
		}
		if strings.Contains(stdout, "repository_id") {
			t.Errorf("%s: stdout = %q", cause, stdout)
		}
	}
}

// Вывод restic со значением из файла окружения не раскрывает его
func TestResticOutputWithAnEnvFileValueDoesNotRevealIt(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].URL = "rest:http://qa:" + urlMarker + "@127.0.0.1:9/main"
	h.saveConfig()
	h.restic.repo(h.cfg.Repositories[0].URL).fatal = "Head http://qa:" + urlMarker + "@127.0.0.1:9/main: key " + envMarker + " rejected"
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitAgentError)
	assertNoSecrets(t, stdout, stderr)
	if !strings.Contains(stderr, "rejected") {
		t.Errorf("the cause from restic is gone: %q", stderr)
	}
}

// Непредусмотренный вывод restic — ошибка агента
func TestUnexpectedResticOutputIsAnAgentError(t *testing.T) {
	h := newRepoHost(t)
	h.main().initNoID = true
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitAgentError)
	if strings.Contains(stdout, "repository_id") || stderr == "" {
		t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
	}
}

// Файл пароля из одних переводов строки отклоняет restic
func TestAPasswordOfOnlyLineBreaksIsRefusedByRestic(t *testing.T) {
	h := newRepoHost(t)
	h.main().fatal = "an empty password is not allowed by default. Pass the flag `--insecure-no-password` to restic to disable this check"
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "PASSWORD_FILE_EMPTY")
}

// Rule "Секреты не попадают в вывод": every class of exit code.
func TestNoSecretReachesTheOutputWhateverTheOutcome(t *testing.T) {
	url := "rest:http://qa:" + urlMarker + "@127.0.0.1:9/main"
	leak := "Head http://qa:" + urlMarker + "@127.0.0.1:9/main " + envMarker
	scenarios := map[string]func(h *repoHost) []string{
		"success": func(h *repoHost) []string { return nil },
		"usage": func(h *repoHost) []string {
			h.write(h.envFile(), "RESTIC_PASSWORD="+envMarker+"\n", 0o600)
			return nil
		},
		"agent error": func(h *repoHost) []string { h.restic.repo(url).fatal = leak; return nil },
		"exists":      func(h *repoHost) []string { h.restic.repo(url).initialized = true; return nil },
		"temporary":   func(h *repoHost) []string { h.restic.repo(url).fatal = leak + " connection refused"; return nil },
		"write": func(h *repoHost) []string {
			h.removePass()
			h.cfg.Repositories[0].PasswordFile = h.path("absent/main.pass")
			return []string{"--generate-password"}
		},
	}
	for name, arrange := range scenarios {
		h := newRepoHost(t)
		h.cfg.Repositories[0].URL = url
		extra := arrange(h)
		h.saveConfig()
		_, stdout, stderr := h.initCmd(extra...)
		assertNoSecrets(t, stdout, stderr)
		if name == "success" && !strings.Contains(stdout, "Initialized") {
			t.Errorf("success: stdout = %q", stdout)
		}
	}
}
