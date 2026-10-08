// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Изменяет хост только sudo, читает и пользователь службы".

// Любая команда от постороннего пользователя — отказ с подсказкой sudo
func TestAnyCommandFromAnotherUserIsRefusedWithTheSudoHint(t *testing.T) {
	for _, args := range [][]string{{"repo", "list"}, {"repo", "init", "base"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newSetupHost(t)
			before := h.hostTree()
			code, stdout, stderr := h.asOther(args...)
			assertCode(t, code, exitUsage)
			assertReason(t, stderr, "PRIVILEGES_REQUIRED")
			if !strings.Contains(stderr, "sudo") || stdout != "" {
				t.Fatalf("stdout %q, stderr %q", stdout, stderr)
			}
			h.assertNoBackendCalls()
			h.assertHostUnchanged(before)
		})
	}
}

// Команда чтения от пользователя службы выполняется
func TestAReadCommandFromTheServiceUserRuns(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.asService("repo", "list")
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "PRIVILEGES_REQUIRED") {
		t.Fatalf("stderr %q", stderr)
	}
}

// Права запуска проверяются раньше конфига
func TestTheRightToRunIsCheckedBeforeTheConfigIsRead(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.asOther("repo", "list", "--config", h.path("no-such-config.yaml"))
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "PRIVILEGES_REQUIRED")
}

// Пользователь службы берётся из ключа service user основного конфига
func TestTheServiceUserComesFromTheServiceUserKeyOfTheMainConfig(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Service.User = "backup"
	h.saveConfig()
	h.base().initialized = true
	h.deps.stat = h.fsys.ownerStat(backupUID)
	code, _, stderr := h.asService("repo", "list")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "PRIVILEGES_REQUIRED") // 990 is not "backup"
	code, _, _ = h.runAs(backupUID, "repo", "list")
	assertCode(t, code, exitOK)
	h.restic.calls = nil
	code, _, _ = h.sudo("repo", "list")
	assertCode(t, code, exitOK)
	for _, c := range h.restic.calls {
		if c.runAs == nil || c.runAs.UID != backupUID {
			t.Fatalf("restic ran as %+v", c.runAs)
		}
	}
}

// Репозиторий без пользователя службы на хосте — отказ
func TestWithoutTheServiceUserOnTheHostRootIsRefused(t *testing.T) {
	h := newSetupHost(t)
	h.people = nil
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "list")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "SERVICE_USER_UNKNOWN")
	if !strings.Contains(stderr, "sard-agent") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

// restic под sudo запускается от имени пользователя службы
func TestResticRunsAsTheServiceUserUnderSudo(t *testing.T) {
	h := newSetupHost(t)
	h.base().initialized = true
	code, _, _ := h.sudo("repo", "list")
	assertCode(t, code, exitOK)
	if len(h.restic.calls) < 2 {
		t.Fatalf("calls %v", h.restic.subs())
	}
	for _, c := range h.restic.calls {
		if c.runAs == nil || c.runAs.UID != serviceUID || c.runAs.GID != serviceUID {
			t.Fatalf("restic %s ran as %+v", c.sub, c.runAs)
		}
	}
}

// restic от пользователя службы запускается как раньше
func TestResticRunsAsTheCallerForTheServiceUser(t *testing.T) {
	h := newSetupHost(t)
	h.asService("repo", "list")
	for _, c := range h.restic.calls {
		if c.runAs != nil {
			t.Fatalf("restic %s ran as %+v", c.sub, c.runAs)
		}
	}
}

// repo init под sudo создаёт файл пароля для пользователя службы
func TestRepoInitUnderSudoCreatesThePasswordFileForTheServiceUser(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories[0].PasswordFile = h.path("secrets/new.pass")
	h.saveConfig()
	h.assertAbsent(h.path("secrets/new.pass"))
	code, _, stderr := h.sudo("repo", "init", "--generate-password", "--config", "C", "base")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertOwner(h.path("secrets/new.pass"), serviceUID)
	h.assertMode(h.path("secrets/new.pass"), 0o600)
	for _, c := range h.restic.calls {
		if c.runAs == nil || c.runAs.UID != serviceUID {
			t.Fatalf("restic %s ran as %+v", c.sub, c.runAs)
		}
	}
}

// repo init от пользователя службы работает как раньше
func TestRepoInitFromTheServiceUserLeavesTheOwnerAlone(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories[0].PasswordFile = h.path("secrets/new.pass")
	h.saveConfig()
	code, _, _ := h.asService("repo", "init", "--generate-password", "--config", "C", "base")
	assertCode(t, code, exitOK)
	if n := h.fsys.chowns(); n != 0 {
		t.Fatalf("%d owner changes", n)
	}
}

// ADR 0050, Р4: the owner is given to the temporary file; the final path is
// never the property of root, not even for a moment.
func TestRepoInitUnderSudoGivesTheOwnerBeforeThePasswordFileTakesItsPath(t *testing.T) {
	h := newSetupHost(t)
	final := h.path("secrets/new.pass")
	h.cfg.Repositories[0].PasswordFile = final
	h.saveConfig()
	code, _, stderr := h.sudo("repo", "init", "--generate-password", "--config", "C", "base")
	assertCode(t, code, exitOK)
	for _, e := range h.fsys.events {
		if strings.HasPrefix(e, "chown "+final+" ") {
			t.Fatalf("the final path was given its owner after it was made: %v", h.fsys.events)
		}
	}
	h.assertOwner(final, serviceUID)
	h.assertMode(final, 0o600)
	h.assertNoTemporaryFiles(h.secretsDir())
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestRepoInitUnderSudoNeverReplacesAPasswordFileThatAppearsMeanwhile(t *testing.T) {
	h := newSetupHost(t)
	final := h.path("secrets/new.pass")
	h.cfg.Repositories[0].PasswordFile = final
	h.saveConfig()
	h.fsys.failOn, h.fsys.failPath = "link", "new.pass"
	code, _, stderr := h.sudo("repo", "init", "--generate-password", "--config", "C", "base")
	assertCode(t, code, exitWrite)
	assertReason(t, stderr, "PASSWORD_FILE_WRITE")
	h.assertAbsent(final)
	h.assertNoTemporaryFiles(h.secretsDir())
}
