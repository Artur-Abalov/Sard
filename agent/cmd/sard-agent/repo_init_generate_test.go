// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Rule "Файл пароля создаётся только по явному флагу".

func (h *repoHost) removePass() {
	h.t.Helper()
	if err := os.Remove(h.pass()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *repoHost) readPass(path string) string {
	h.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

// С флагом генерации отсутствующий файл пароля создаётся и репозиторий инициализируется
func TestWithTheGenerateFlagAMissingPasswordFileIsCreatedAndTheRepositoryInitialised(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	code, _, stderr := h.initCmd("--generate-password")
	assertCode(t, code, exitOK)
	if h.readPass(h.pass()) == "" {
		t.Fatal("the password file is empty")
	}
	calls := h.restic.callsTo(h.repoURL(), "init")
	if len(calls) != 1 || calls[0].passwordFile != h.pass() {
		t.Fatalf("calls = %+v, stderr = %q", calls, stderr)
	}
}

// Созданный файл пароля закрыт от всех, кроме владельца, при любой umask
func TestTheCreatedPasswordFileIsOwnerOnlyWhateverTheUmask(t *testing.T) {
	for _, umask := range []int{0o000, 0o277} {
		old := syscall.Umask(umask)
		h := newRepoHost(t)
		h.removePass()
		code, _, _ := h.initCmd("--generate-password")
		syscall.Umask(old)
		assertCode(t, code, exitOK)
		info, err := os.Stat(h.pass())
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("umask %o: %v, %v", umask, info, err)
		}
		if uid := info.Sys().(*syscall.Stat_t).Uid; uid != uint32(os.Getuid()) {
			t.Errorf("owner = %d", uid)
		}
	}
}

// Созданный пароль — 43 символа base64url и перевод строки
func TestTheCreatedPasswordIs43Base64urlCharactersAndALineBreak(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	h.initCmd("--generate-password")
	if got := h.readPass(h.pass()); !regexp.MustCompile(`^[A-Za-z0-9_-]{43}\n$`).MatchString(got) {
		t.Fatalf("password file = %q", got)
	}
}

// Два созданных пароля различаются
func TestTwoCreatedPasswordsDiffer(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	if err := os.Remove(h.pass2()); err != nil {
		t.Fatal(err)
	}
	assertCode(t, first(h.initCmd("--generate-password")), exitOK)
	code, _, _ := h.run("init", "--config", "C", "--generate-password", "offsite")
	assertCode(t, code, exitOK)
	if h.readPass(h.pass()) == h.readPass(h.pass2()) {
		t.Fatal("the two passwords are equal")
	}
}

func first(code int, _, _ string) int { return code }

// Созданный пароль не попадает в вывод
func TestTheCreatedPasswordIsNotPrinted(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	_, stdout, stderr := h.initCmd("--generate-password")
	password := strings.TrimSpace(h.readPass(h.pass()))
	if strings.Contains(stdout, password) || strings.Contains(stderr, password) {
		t.Fatal("the password is in the output")
	}
}

// Итог успеха говорит, что пароль сгенерирован
func TestTheSummarySaysThePasswordWasGenerated(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	_, stdout, _ := h.initCmd("--generate-password")
	if !strings.Contains(stdout, h.pass()+" (created by this command)") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// Существующий файл пароля с флагом генерации не перезаписывается
func TestAnExistingPasswordFileIsNotOverwrittenByTheGenerateFlag(t *testing.T) {
	h := newRepoHost(t)
	before := h.snapshot()
	code, stdout, _ := h.initCmd("--generate-password")
	assertCode(t, code, exitOK)
	if h.readPass(h.pass()) != passMarker+"\n" {
		t.Fatal("the password file was overwritten")
	}
	if !strings.Contains(stdout, "existing password file used") {
		t.Errorf("stdout = %q", stdout)
	}
	after := h.snapshot()
	if after[h.pass()] != before[h.pass()] {
		t.Fatal("the mode or owner of the password file changed")
	}
}

// Существующий файл пароля с широкими правами флаг генерации не спасает
func TestTheGenerateFlagDoesNotRescueAWidePasswordFile(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Chmod(h.pass(), 0o644); err != nil {
		t.Fatal(err)
	}
	before := h.snapshot()
	code, _, _ := h.initCmd("--generate-password")
	assertCode(t, code, exitUsage)
	h.assertUnchanged(before)
	h.assertNoBackendCalls()
}

// Файл пароля нельзя создать — ошибка записи
func TestAPasswordFileThatCannotBeCreatedIsAWriteError(t *testing.T) {
	missingDir := func(h *repoHost) string {
		h.cfg.Repositories[0].PasswordFile = h.path("no-such-dir/main.pass")
		return h.path("no-such-dir")
	}
	unwritable := func(h *repoHost) string {
		h.deps.writeNew = func(string, []byte) error { return &os.PathError{Op: "open", Path: h.pass(), Err: syscall.EACCES} }
		h.removePass()
		return h.dir
	}
	for name, arrange := range map[string]func(*repoHost) string{"missing": missingDir, "unwritable": unwritable} {
		h := newRepoHost(t)
		dir := arrange(h)
		h.saveConfig()
		if name == "missing" {
			h.removePass()
		}
		code, _, stderr := h.initCmd("--generate-password")
		assertCode(t, code, exitWrite)
		assertReason(t, stderr, "PASSWORD_FILE_WRITE")
		if !strings.Contains(stderr, dir) {
			t.Errorf("%s: stderr does not name %s: %s", name, dir, stderr)
		}
		h.assertNoBackendCalls()
		if _, err := os.Stat(h.path("no-such-dir")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: the directory was created", name)
		}
	}
}

// Файл пароля не создаётся, если restic непригоден
func TestThePasswordFileIsNotCreatedWhenResticIsUnusable(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	h.cfg.Restic.Path = h.path("nowhere/restic")
	h.saveConfig()
	code, _, stderr := h.initCmd("--generate-password")
	assertCode(t, code, exitAgentError)
	assertReason(t, stderr, "RESTIC_NOT_FOUND")
	if _, err := os.Stat(h.pass()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the password file was created")
	}
}

// Созданный файл пароля остаётся после неудачи инициализации
func TestTheCreatedPasswordFileStaysAfterAFailedInitialisation(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	h.main().fatal = "dial tcp 127.0.0.1:9: connect: connection refused"
	code, _, stderr := h.initCmd("--generate-password")
	assertCode(t, code, exitTemporary)
	info, err := os.Stat(h.pass())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v, %v", info, err)
	}
	if !strings.Contains(stderr, "password file "+h.pass()+" created by this command was kept and will be used when the command is repeated") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Файл пароля, созданный командой, проходит проверку при старте агента
func TestThePasswordFileOfTheCommandPassesTheChecksOfTheAgentStart(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	assertCode(t, first(h.initCmd("--generate-password")), exitOK)
	cfg, err := config.Load(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.CheckAll(cfg, uint32(os.Getuid()), secrets.RealStat); err != nil {
		t.Fatalf("A1 at start: %v", err)
	}
}

// Истечение таймаута сохраняет созданный файл пароля
func TestATimeoutKeepsTheCreatedPasswordFile(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	h.main().hangInit = true
	done := make(chan int, 1)
	go func() { code, _, _ := h.initCmd("--generate-password"); done <- code }()
	<-h.main().initEntered
	h.clock.fireNow()
	assertCode(t, <-done, exitTemporary)
	info, err := os.Stat(h.pass())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v, %v", info, err)
	}
}
