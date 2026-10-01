// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scenarios of docs/specs/agent/repo-init.feature, rule "Один
// репозиторий на хосте инициализирует одна команда", amendment В8а.

const lockPrefix = ".sard-init-"

// lockFiles are the files whose name starts with ".sard-init-" in dir.
func lockFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), lockPrefix) {
			got = append(got, e.Name())
		}
	}
	return got
}

// assertNoLockFiles is "Файлов блокировки нет": neither in K nor next to P.
func (h *repoHost) assertNoLockFiles() {
	h.t.Helper()
	for _, dir := range []string{h.cacheDir(), filepath.Dir(h.pass())} {
		if got := lockFiles(h.t, dir); len(got) != 0 {
			h.t.Errorf("lock files in %s: %v", dir, got)
		}
	}
}

// Файл блокировки создаётся в каталоге кэша restic, а не рядом с файлом пароля
func TestTheLockFileIsCreatedInTheCacheDirNotNextToThePasswordFile(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	ctx, stop := context.WithCancel(context.Background())
	first := h.start(ctx, "init", "--config", "C", "main")
	<-h.main().initEntered
	if got := lockFiles(t, h.cacheDir()); len(got) != 1 || got[0] != ".sard-init-main.lock" {
		t.Errorf("lock files in K: %v", got)
	}
	if got := lockFiles(t, filepath.Dir(h.pass())); len(got) != 0 {
		t.Errorf("lock files next to P: %v", got)
	}
	stop()
	within(t, first)
}

// Файл блокировки не остаётся после завершения команды
func TestTheLockFileIsGoneWhenTheCommandEnds(t *testing.T) {
	outcomes := map[string]func(h *repoHost) []string{
		"success": func(h *repoHost) []string { return nil },
		"REPOSITORY_EXISTS": func(h *repoHost) []string {
			h.main().initialized = true
			return nil
		},
		"WRONG_PASSWORD": func(h *repoHost) []string { h.main().wrongPassword = true; return nil },
		"BACKEND_UNAVAILABLE": func(h *repoHost) []string {
			h.main().fatal = "unable to open repository: connection refused"
			return nil
		},
		"BACKEND_REFUSED": func(h *repoHost) []string { h.main().fatal = "unable to open repository: 403 Forbidden"; return nil },
		"PASSWORD_FILE_WRITE": func(h *repoHost) []string {
			h.cfg.Repositories[0].PasswordFile = h.path("no-such-dir/main.pass")
			h.saveConfig()
			return []string{"--generate-password"}
		},
	}
	for name, arrange := range outcomes {
		h := newRepoHost(t)
		extra := arrange(h)
		h.initCmd(extra...)
		if got := lockFiles(t, h.cacheDir()); len(got) != 0 {
			t.Errorf("%s: lock files left in K: %v", name, got)
		}
		h.assertNoLockFiles()
	}
}

// Файл блокировки, оставшийся после аварийного завершения, не мешает повтору
func TestAStaleLockFileInTheCacheDirDoesNotBlockARepeat(t *testing.T) {
	h := newRepoHost(t)
	h.write(filepath.Join(h.cacheDir(), ".sard-init-main.lock"), "4242\n", 0o600)
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	assertNoLock(t, stderr)
	h.assertNoLockFiles()
}

func assertNoLock(t *testing.T, out string) {
	t.Helper()
	assertReasonAbsent(t, out, "INIT_IN_PROGRESS")
	assertReasonAbsent(t, out, "LOCK_WRITE")
}

func assertReasonAbsent(t *testing.T, out, reason string) {
	t.Helper()
	if strings.Contains(out, reason) {
		t.Fatalf("output names %s:\n%s", reason, out)
	}
}

// Существующий файл пароля в каталоге только для чтения не мешает инициализации
func TestAnExistingPasswordFileInAReadOnlyDirectoryDoesNotHinderInit(t *testing.T) {
	for _, flags := range [][]string{nil, {"--generate-password"}} {
		h := newRepoHost(t)
		dir := h.path("secrets")
		h.write(filepath.Join(dir, "main.pass"), passMarker+"\n", 0o600)
		h.cfg.Repositories[0].PasswordFile = filepath.Join(dir, "main.pass")
		h.saveConfig()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		before := h.snapshot()
		code, _, stderr := h.initCmd(flags...)
		assertCode(t, code, exitOK)
		assertNoLock(t, stderr)
		assertReasonAbsent(t, stderr, "PASSWORD_FILE_WRITE")
		h.assertUnchangedWithout(before, h.repoURL())
	}
}

// Файл блокировки нельзя создать — ошибка записи
func TestALockFileThatCannotBeCreatedIsAWriteError(t *testing.T) {
	states := map[string]func(h *repoHost){
		"missing": func(h *repoHost) {
			if err := os.Remove(h.cacheDir()); err != nil {
				h.t.Fatal(err)
			}
		},
		"regular file": func(h *repoHost) {
			if err := os.Remove(h.cacheDir()); err != nil {
				h.t.Fatal(err)
			}
			h.write(h.cacheDir(), "", 0o600)
		},
		"read only": func(h *repoHost) {
			if err := os.Chmod(h.cacheDir(), 0o500); err != nil {
				h.t.Fatal(err)
			}
		},
	}
	for name, arrange := range states {
		h := newRepoHost(t)
		arrange(h)
		before := h.snapshot()
		code, _, stderr := h.initCmd()
		assertCode(t, code, exitWrite)
		assertReason(t, stderr, "LOCK_WRITE")
		assertReason(t, stderr, "restic.cache_dir")
		assertReason(t, stderr, h.cacheDir())
		if name == "read only" {
			assertReason(t, stderr, "permission denied")
		}
		h.assertNoBackendCalls()
		h.assertUnchanged(before)
		_ = os.Chmod(h.cacheDir(), 0o700)
	}
}

// Отсутствующий каталог кэша restic не создаётся
func TestAMissingCacheDirIsNotCreated(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.cacheDir()); err != nil {
		t.Fatal(err)
	}
	_, _, stderr := h.initCmd()
	assertReason(t, stderr, "LOCK_WRITE")
	if _, err := os.Stat(h.cacheDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat K: %v", err)
	}
}

// Без ключа каталога кэша в конфиге блокировка берётся в каталоге кэша по умолчанию
func TestWithoutACacheDirKeyTheDefaultCacheDirHoldsTheLock(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Restic.CacheDir = ""
	h.saveConfig()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitWrite)
	assertReason(t, stderr, "LOCK_WRITE")
	assertReason(t, stderr, h.deps.defaultCacheDir)
}

// Файл пароля не создаётся, если блокировку не взять
func TestThePasswordFileIsNotCreatedWhenTheLockCannotBeTaken(t *testing.T) {
	h := newRepoHost(t)
	h.removePass()
	if err := os.Remove(h.cacheDir()); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.initCmd("--generate-password")
	assertCode(t, code, exitWrite)
	assertReason(t, stderr, "LOCK_WRITE")
	if _, err := os.Stat(h.pass()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat P: %v", err)
	}
}

// Каталог кэша по умолчанию — каталог кэша службы
func TestTheProductionDefaultCacheDirIsTheServiceCacheDir(t *testing.T) {
	if got := productionRepoDeps().defaultCacheDir; got != "/var/cache/sard/restic" {
		t.Fatalf("default cache dir = %q", got)
	}
}

// Список не берёт блокировку и не ждёт идущей инициализации
func TestTheListTakesNoLockAndDoesNotWaitForARunningInit(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	ctx, stop := context.WithCancel(context.Background())
	first := h.start(ctx, "init", "--config", "C", "main")
	<-h.main().initEntered
	code, stdout, stderr := h.listCmd()
	assertCode(t, code, exitOK)
	assertNoLock(t, stdout+stderr)
	stop()
	within(t, first)
}

// Список работает без каталога кэша restic
func TestTheListWorksWithoutTheCacheDir(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.cacheDir()); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := h.listCmd()
	assertCode(t, code, exitOK)
	assertNoLock(t, stdout+stderr)
	if _, err := os.Stat(h.cacheDir()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat K: %v", err)
	}
}

// The help of repo init names the write reasons.
func TestTheInitHelpNamesBothWriteReasons(t *testing.T) {
	h := newRepoHost(t)
	_, stdout, _ := h.run("init", "--help")
	for _, want := range []string{"password file", "restic.cache_dir"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help does not contain %q:\n%s", want, stdout)
		}
	}
}
