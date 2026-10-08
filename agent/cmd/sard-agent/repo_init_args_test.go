// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Rule "Репозиторий выбирается по имени из конфига".

// Неизвестное имя репозитория — ошибка со списком известных имён
func TestAnUnknownRepositoryNameIsAnErrorListingTheKnownNames(t *testing.T) {
	h := newRepoHost(t)
	before := h.snapshot()
	code, stdout, stderr := h.run("init", "--config", "C", "backup")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "REPOSITORY_UNKNOWN")
	for _, want := range []string{"backup", "main", "offsite"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not name %q: %s", want, stderr)
		}
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}
	h.assertNoBackendCalls()
	h.assertUnchanged(before)
}

// Имя репозитория сравнивается с учётом регистра
func TestTheRepositoryNameIsCaseSensitive(t *testing.T) {
	h := newRepoHost(t)
	code, _, stderr := h.run("init", "--config", "C", "Main")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "REPOSITORY_UNKNOWN")
}

// Конфиг без репозиториев — сообщение говорит, что их нет
func TestAConfigWithoutRepositoriesSaysSo(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories = nil
	h.saveConfig()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "REPOSITORY_UNKNOWN")
	if !strings.Contains(stderr, "no repositories configured in "+h.cfgPath) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Rule "Флаги и конфиг проверяются до обращения к бэкенду".

// Справка описывает подкоманду, флаги и коды выхода
func TestHelpDescribesTheSubcommandTheFlagsAndTheExitCodes(t *testing.T) {
	h := newRepoHost(t)
	code, stdout, _ := h.run("init", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{
		"Creates the repository named <name> in the agent config",
		"--config", "--generate-password", "--timeout", "/etc/sard/agent.yaml", "2m",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not contain %q:\n%s", want, stdout)
		}
	}
	assertDescribesCodes(t, stdout, []int{exitOK, exitAgentError, exitUsage, exitIdentityExists, exitTemporary, exitWrite}, []int{exitTokenRefused, exitTrust})
	for _, bad := range []string{"--password", "--key", "--repo "} {
		if strings.Contains(stdout, bad) {
			t.Errorf("help names %q", bad)
		}
	}
	h.assertNoBackendCalls()
}

// Ошибка вызова — ошибка использования
func TestAMalformedInvocationIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "C"},
		{"init", "--config", "C"},
		{"init", "--config", "C", "main", "extra"},
		{"init", "--config", "C", "--insecure", "main"},
		{"frobnicate", "--config", "C", "main"},
		{},
	} {
		h := newRepoHost(t)
		code, stdout, stderr := h.run(args...)
		if code != exitUsage || stdout != "" || stderr == "" {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
		h.assertNoBackendCalls()
	}
}

// Пароль и ключи флагами не принимаются
func TestPasswordsAndKeysAreNotAcceptedAsFlags(t *testing.T) {
	for _, extra := range [][]string{
		{"--password", passMarker},
		{"--password-file", "P"},
		{"--repo", "R"},
	} {
		h := newRepoHost(t)
		args := append([]string{"init", "--config", "C"}, append(extra, "main")...)
		code, stdout, stderr := h.run(args...)
		assertCode(t, code, exitUsage)
		h.assertNoBackendCalls()
		assertNoSecrets(t, stdout, stderr)
	}
}

// Флаги принимаются и после имени репозитория
func TestFlagsAreAcceptedAfterTheRepositoryName(t *testing.T) {
	h := newRepoHost(t)
	code, _, stderr := h.run("init", "main", "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Без флага конфига используется конфиг службы по умолчанию
func TestWithoutTheConfigFlagTheServiceConfigIsUsed(t *testing.T) {
	h := newRepoHost(t)
	if got := productionHostDeps().defaultConfig; got != "/etc/sard/agent.yaml" {
		t.Fatalf("default config = %q", got)
	}
	h.deps.defaultConfig = h.cfgPath
	code, _, _ := h.run("init", "main")
	assertCode(t, code, exitOK)
	if len(h.restic.callsTo(h.repoURL(), "init")) != 1 {
		t.Fatal("main was not taken from the default config")
	}
}

// Отсутствующий конфиг — ошибка использования
func TestAMissingConfigIsAUsageError(t *testing.T) {
	h := newRepoHost(t)
	missing := h.path("nope/agent.yaml")
	code, _, stderr := h.run("init", "--config", missing, "main")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, missing) {
		t.Fatalf("stderr = %q", stderr)
	}
	h.assertNoBackendCalls()
}

// Невалидный конфиг — ошибка использования
func TestAnInvalidConfigIsAUsageError(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].URL = ""
	h.saveConfig()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "url") || !strings.Contains(stderr, "main") {
		t.Fatalf("stderr = %q", stderr)
	}
	h.assertNoBackendCalls()
}

// Недопустимый таймаут — ошибка использования
func TestAnInvalidTimeoutIsAUsageError(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "abc"} {
		h := newRepoHost(t)
		code, _, stderr := h.initCmd("--timeout", value)
		assertCode(t, code, exitUsage)
		if stderr == "" {
			t.Errorf("%s: no message", value)
		}
		h.assertNoBackendCalls()
	}
}

// describesCode: --help has a line "  <code>  <word>: ..." for the code.
func describesCode(help string, code int) bool {
	return regexp.MustCompile(`(?m)^  ` + strconv.Itoa(code) + `  \S`).MatchString(help)
}

func assertDescribesCodes(t *testing.T, help string, want, absent []int) {
	t.Helper()
	for _, c := range want {
		if !describesCode(help, c) {
			t.Errorf("help does not describe exit code %d:\n%s", c, help)
		}
	}
	for _, c := range absent {
		if describesCode(help, c) {
			t.Errorf("help describes exit code %d, which the command never returns", c)
		}
	}
}
