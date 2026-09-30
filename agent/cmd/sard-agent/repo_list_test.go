// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// listRows parses the table of repo list: header, then one row per repository.
func listRows(t *testing.T, stdout string) map[string][]string {
	t.Helper()
	rows := map[string][]string{}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "NAME BACKEND STATUS REPOSITORY_ID" {
		t.Fatalf("header = %q", lines[0])
	}
	for _, l := range lines[1:] {
		fields := strings.Fields(l)
		if len(fields) != 4 {
			t.Fatalf("row %q has %d columns", l, len(fields))
		}
		rows[fields[0]] = fields
	}
	return rows
}

// Rule "Список показывает состояние каждого репозитория конфига".

// Список называет инициализированный и неинициализированный репозитории
func TestTheListNamesInitialisedAndNotInitialisedRepositories(t *testing.T) {
	h := newRepoHost(t)
	h.main().initialized = true
	code, stdout, stderr := h.listCmd()
	assertCode(t, code, exitOK)
	rows := listRows(t, stdout)
	if got := strings.Join(rows["main"], " "); got != "main local initialized "+h.main().id {
		t.Errorf("main = %q", got)
	}
	if got := strings.Join(rows["offsite"], " "); got != "offsite rest not-initialized -" {
		t.Errorf("offsite = %q", got)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

// Строки списка идут в порядке конфига
func TestTheListRowsFollowTheOrderOfTheConfig(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0], h.cfg.Repositories[1] = h.cfg.Repositories[1], h.cfg.Repositories[0]
	h.saveConfig()
	_, stdout, _ := h.listCmd()
	if strings.Index(stdout, "offsite") > strings.Index(stdout, "main") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Список не печатает адреса репозиториев
func TestTheListDoesNotPrintRepositoryAddresses(t *testing.T) {
	h := newRepoHost(t)
	_, stdout, stderr := h.listCmd()
	for _, r := range h.cfg.Repositories {
		if strings.Contains(stdout, r.URL) || strings.Contains(stderr, r.URL) {
			t.Errorf("the address %s is printed", r.URL)
		}
	}
	assertNoSecrets(t, stdout, stderr)
}

// Список ничего не создаёт и не меняет
func TestTheListCreatesAndChangesNothing(t *testing.T) {
	h := newRepoHost(t)
	before := h.snapshot()
	code, _, _ := h.listCmd()
	assertCode(t, code, exitOK)
	for _, sub := range h.restic.subs() {
		if sub == "init" {
			t.Fatal("restic init was called")
		}
	}
	h.assertUnchanged(before)
}

// Конфиг без репозиториев — пустой список и успех
func TestAConfigWithoutRepositoriesGivesAnEmptyListAndSuccess(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories = nil
	h.saveConfig()
	code, stdout, _ := h.listCmd()
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "no repositories configured") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Список не читает стандартный ввод и не обращается к серверу Sard
func TestTheListDoesNotReadStdinAndDoesNotConnectToTheServer(t *testing.T) {
	h := newRepoHost(t)
	addr, accepted := countingServer(t)
	h.cfg.Server.Address = addr
	h.saveConfig()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(); _ = r.Close() }()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	done := h.start(context.Background(), "list", "--config", "C")
	assertCode(t, within(t, done).code, exitOK)
	if accepted.Load() != 0 {
		t.Fatal("the list connected to server.address")
	}
}

// Справка списка описывает колонки, флаги и коды выхода
func TestTheListHelpDescribesColumnsFlagsAndExitCodes(t *testing.T) {
	h := newRepoHost(t)
	code, stdout, _ := h.run("list", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{
		"NAME", "BACKEND", "STATUS", "REPOSITORY_ID", "initialized", "not-initialized",
		"--config", "--timeout", "first problem row",
		"BACKEND_REFUSED", "RESTIC_OUTPUT_UNEXPECTED",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not contain %q:\n%s", want, stdout)
		}
	}
	assertDescribesCodes(t, stdout, []int{exitOK, exitAgentError, exitUsage, exitTemporary}, []int{exitTokenRefused, exitTrust, exitIdentityExists, exitWrite})
	if strings.Contains(stdout, "--generate-password") {
		t.Error("help names a flag of repo init")
	}
}

// Ошибка вызова списка — ошибка использования
func TestAMalformedListInvocationIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--config", "C", "main"},
		{"list", "--config", "C", "--json"},
		{"list", "--config", "C", "--timeout", "0s"},
		{"list", "--config", "C", "--generate-password"},
	} {
		h := newRepoHost(t)
		code, stdout, _ := h.run(args...)
		if code != exitUsage || stdout != "" {
			t.Errorf("%v: code = %d, stdout = %q", args, code, stdout)
		}
		h.assertNoBackendCalls()
	}
}

// Отсутствующий конфиг — список не печатается
func TestAMissingConfigPrintsNoList(t *testing.T) {
	h := newRepoHost(t)
	code, stdout, _ := h.run("list", "--config", h.path("nope.yaml"))
	assertCode(t, code, exitUsage)
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Rule "Проблема одного репозитория не прерывает список".

// Проблемная строка получает причину, остальные строки печатаются
func TestAProblemRowGetsAReasonAndTheOtherRowsArePrinted(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(h *repoHost)
		status  string
		code    int
	}{
		{"no password file", func(h *repoHost) { _ = os.Remove(h.pass2()) }, "PASSWORD_FILE_MISSING", exitUsage},
		{"empty password file", func(h *repoHost) { h.write(h.pass2(), "", 0o600) }, "PASSWORD_FILE_EMPTY", exitUsage},
		{"password file 0644", func(h *repoHost) { _ = os.Chmod(h.pass2(), 0o644) }, "SECRET_FILE_REJECTED", exitUsage},
		{"crypto gost", func(h *repoHost) { h.cfg.Repositories[1].CryptoProvider = "gost"; h.saveConfig() }, "CRYPTO_PROVIDER_UNSUPPORTED", exitUsage},
		{"wrong password", func(h *repoHost) { h.offsite().wrongPassword = true }, "WRONG_PASSWORD", exitUsage},
		{"access denied", func(h *repoHost) { h.offsite().fatal = "Access Denied" }, "BACKEND_REFUSED", exitAgentError},
		{"unreachable", func(h *repoHost) { h.offsite().fatal = "dial tcp: connection refused" }, "BACKEND_UNAVAILABLE", exitTemporary},
	}
	for _, c := range cases {
		h := newRepoHost(t)
		h.main().initialized = true
		c.arrange(h)
		code, stdout, stderr := h.listCmd()
		assertCode(t, code, c.code)
		rows := listRows(t, stdout)
		if rows["offsite"][2] != c.status || rows["offsite"][3] != "-" {
			t.Errorf("%s: offsite = %q", c.name, rows["offsite"])
		}
		if rows["main"][2] != "initialized" || rows["main"][3] != h.main().id {
			t.Errorf("%s: main = %q", c.name, rows["main"])
		}
		assertRowMessage(t, c.name, stderr, "offsite", c.status)
		assertNoSecrets(t, stdout, stderr)
	}
}

// assertRowMessage: stderr has a message that starts with the repository
// name and names the status (A1's text stands for SECRET_FILE_REJECTED).
func assertRowMessage(t *testing.T, name, stderr, repo, status string) {
	t.Helper()
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, repo) && (status == "SECRET_FILE_REJECTED" || strings.Contains(l, status)) {
			return
		}
	}
	t.Errorf("%s: no message for %s naming %s in %q", name, repo, status, stderr)
}

// Для строки с проблемой до бэкенда restic не вызывается
func TestNoResticCallIsMadeForARowWithAProblemBeforeTheBackend(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.pass2()); err != nil {
		t.Fatal(err)
	}
	h.listCmd()
	if n := len(h.restic.callsTo(offsiteURL, "cat")); n != 0 {
		t.Fatalf("restic was called %d times for offsite", n)
	}
}

// Нарушение прав в строке списка поясняется текстом A1
func TestAPermissionProblemInARowIsExplainedWithTheA1Text(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Chmod(h.pass2(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, stderr := h.listCmd()
	want := "offsite: secret file repositories[1].password_file (" + h.pass2() + ") has mode -rw-r--r--"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

// Постоянная проблема строки важнее временной
func TestAPermanentRowProblemBeatsATemporaryOne(t *testing.T) {
	h := newRepoHost(t)
	h.main().fatal = "connection refused"
	if err := os.Remove(h.pass2()); err != nil {
		t.Fatal(err)
	}
	code, _, _ := h.listCmd()
	assertCode(t, code, exitUsage)
}

// Код выхода берётся из первой по порядку конфига постоянной проблемы
func TestTheExitCodeComesFromTheFirstPermanentProblemInConfigOrder(t *testing.T) {
	h := newRepoHost(t)
	h.main().fatal = "Access Denied"
	if err := os.Remove(h.pass2()); err != nil {
		t.Fatal(err)
	}
	code, _, _ := h.listCmd()
	assertCode(t, code, exitAgentError)
}

// Только временные проблемы строк — временная ошибка
func TestOnlyTemporaryRowProblemsGiveATemporaryError(t *testing.T) {
	h := newRepoHost(t)
	h.main().fatal = "connection refused"
	h.offsite().fatal = "no such host"
	code, stdout, _ := h.listCmd()
	assertCode(t, code, exitTemporary)
	rows := listRows(t, stdout)
	if rows["main"][2] != "BACKEND_UNAVAILABLE" || rows["offsite"][2] != "BACKEND_UNAVAILABLE" {
		t.Fatalf("rows = %v", rows)
	}
}

// Неинициализированные репозитории не делают список ошибкой
func TestRepositoriesThatAreNotInitialisedAreNoError(t *testing.T) {
	h := newRepoHost(t)
	code, _, _ := h.listCmd()
	assertCode(t, code, exitOK)
}

// Истечение таймаута списка отмечает незавершённые строки
func TestATimeoutOfTheListMarksTheUnfinishedRows(t *testing.T) {
	h := newRepoHost(t)
	h.main().initialized = true
	h.offsite().hangCat = true
	done := h.start(context.Background(), "list", "--config", "C")
	for len(h.restic.callsTo(offsiteURL, "cat")) == 0 {
		yield()
	}
	h.clock.fireNow()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	rows := listRows(t, r.stdout)
	if rows["main"][2] != "initialized" || rows["main"][3] != h.main().id || rows["offsite"][2] != "TIMEOUT" {
		t.Fatalf("rows = %v", rows)
	}
	if !h.offsite().terminated {
		t.Error("restic for offsite did not get SIGTERM")
	}
}

// Прерывание списка не печатает таблицу
func TestAnInterruptOfTheListPrintsNoTable(t *testing.T) {
	h := newRepoHost(t)
	h.offsite().hangCat = true
	ctx, interrupt := context.WithCancel(context.Background())
	done := h.start(ctx, "list", "--config", "C")
	for len(h.restic.callsTo(offsiteURL, "cat")) == 0 {
		yield()
	}
	interrupt()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	assertReason(t, r.stderr, "INTERRUPTED")
	if strings.Contains(r.stdout, "NAME") {
		t.Fatalf("stdout = %q", r.stdout)
	}
}

// Rule "Список проверяет restic один раз до строк".

// Непригодный restic — список не печатается
func TestAnUnusableResticPrintsNoList(t *testing.T) {
	dir := func(h *repoHost) { h.cfg.Restic.Path = h.dir; h.deps.exec = restic.ProcessExecutor{} }
	cases := map[string]struct {
		arrange func(h *repoHost)
		reason  string
	}{
		"not found": {func(h *repoHost) { h.cfg.Restic.Path = h.path("nowhere/restic") }, "RESTIC_NOT_FOUND"},
		"too old":   {func(h *repoHost) { h.restic.version = "0.18.1" }, "RESTIC_TOO_OLD"},
		"directory": {dir, "RESTIC_UNUSABLE"},
	}
	for name, c := range cases {
		h := newRepoHost(t)
		c.arrange(h)
		h.saveConfig()
		code, stdout, stderr := h.listCmd()
		assertCode(t, code, exitAgentError)
		assertReason(t, stderr, c.reason)
		if stdout != "" {
			t.Errorf("%s: stdout = %q", name, stdout)
		}
		h.assertNoBackendCalls()
	}
}
