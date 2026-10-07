// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// set is "secret set <name> --stdin" with the value on standard input.
func (h *setupHost) set(name, value string, extra ...string) (int, string, string) {
	h.stdinIs(value)
	// The name stands after "--": one that starts with "-" is a name, not a flag.
	args := append([]string{"secret", "set"}, extra...)
	return h.sudo(append(args, "--stdin", "--config", "C", "--", name)...)
}

func assertRefusal(t *testing.T, code int, stderr string, wantCode int, reason string) {
	t.Helper()
	assertCode(t, code, wantCode)
	assertReason(t, stderr, reason)
}

// Rule "Секрет передаётся только из файла, стандартного ввода или терминала без эха".

func TestStdinValueIsStoredByteForByte(t *testing.T) {
	h := newSetupHost(t)
	value := "SECRET-MARKER\n"
	code, _, _ := h.set("db", value)
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != value {
		t.Fatalf("stored %q", got)
	}
}

func TestFileValueIsStoredByteForByteAndTheSourceIsUntouched(t *testing.T) {
	h := newSetupHost(t)
	src := h.path("outside/F")
	h.write(src, "SECRET-MARKER\n", 0o644)
	before := h.fsys.chowns()
	code, _, _ := h.sudo("secret", "set", "db", "--from-file", src, "--config", "C")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER\n" {
		t.Fatalf("stored %q", got)
	}
	if _, ok := h.fsys.ownerOf(src); ok || h.fileContent(src) != "SECRET-MARKER\n" || before > h.fsys.chowns() {
		t.Fatal("the source file was touched")
	}
	h.assertMode(src, 0o644)
}

func TestTerminalValueIsAskedTwiceAndStoredWithoutALineBreak(t *testing.T) {
	h := newSetupHost(t)
	term := h.terminalIs("SECRET-MARKER", "SECRET-MARKER")
	code, _, stderr := h.sudo("secret", "set", "db", "--config", "C")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER" || len(term.prompts) != 2 {
		t.Fatalf("stored %q after %d prompts", got, len(term.prompts))
	}
	h.assertValuesHidden(stderr)
}

func TestTerminalValuesThatDifferAreRefusedAndNothingChanges(t *testing.T) {
	h := newSetupHost(t)
	h.terminalIs("SECRET-MARKER-1", "SECRET-MARKER-2")
	before := h.hostTree()
	code, stdout, stderr := h.sudo("secret", "set", "db", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_MISMATCH")
	h.assertHostUnchanged(before)
	h.assertValuesHidden(stdout, stderr)
}

func TestWithoutATerminalAndWithoutAFlagNothingIsRead(t *testing.T) {
	h := newSetupHost(t)
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	before := h.hostTree()
	code, _, stderr := h.sudo("secret", "set", "db", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_MISSING")
	for _, want := range []string{"--stdin", "--from-file"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %s: %q", want, stderr)
		}
	}
	if h.hung.reads != 0 {
		t.Fatal("the input was read")
	}
	h.assertHostUnchanged(before)
}

func TestTwoSourcesAreRefused(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.sudo("secret", "set", "db", "--stdin", "--from-file", h.path("F"), "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_CONFLICT")
	h.assertHostUnchanged(before)
}

func TestAValueGivenAsAnArgumentOrFlagIsNotAcceptedNorPrinted(t *testing.T) {
	for _, args := range [][]string{
		{"secret", "set", "db", "SECRET-MARKER", "--config", "C"},
		{"secret", "set", "db", "--value", "SECRET-MARKER", "--config", "C"},
		{"secret", "set", "db", "--value=SECRET-MARKER", "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sudo(args...)
		assertCode(t, code, exitUsage)
		h.assertValuesHidden(stdout, stderr)
		h.assertHostUnchanged(before)
	}
}

func TestEmptyValueIsRefused(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.set("db", "")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_EMPTY")
	h.assertHostUnchanged(before)
}

func TestValueOver64KiBIsRefusedAndExactly64KiBIsAccepted(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.set("db", strings.Repeat("a", 65537))
	assertRefusal(t, code, stderr, exitUsage, "SECRET_TOO_LARGE")
	if !strings.Contains(stderr, "65536") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
	code, _, _ = h.set("db", strings.Repeat("a", 65536))
	assertCode(t, code, exitOK)
}

func TestMissingValueFileIsRefusedWithItsPath(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	missing := h.path("nowhere/F")
	code, _, stderr := h.sudo("secret", "set", "db", "--from-file", missing, "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, missing) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

// Rule "secret set добавляет секрет отдельными файлами с правами A1".

func TestNewSecretIsAValueFileAndAFragment(t *testing.T) {
	h := newSetupHost(t)
	mainBefore := h.fileContent(h.cfgPath)
	code, stdout, stderr := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	value, fragment := h.path("secrets/db"), h.path("agent.d/secret-db.yaml")
	h.assertOwner(value, serviceUID)
	h.assertMode(value, 0o600)
	if o, _ := h.fsys.ownerOf(fragment); o != (ownerRec{0, serviceUID}) {
		t.Fatalf("fragment owner %+v", o)
	}
	h.assertMode(fragment, 0o640)
	if o, _ := h.fsys.ownerOf(h.agentD()); o != (ownerRec{0, serviceUID}) {
		t.Fatalf("agent.d owner %+v", o)
	}
	h.assertMode(h.agentD(), 0o750)
	if body := h.fileContent(fragment); !strings.Contains(body, "secrets:") || !strings.Contains(body, "db: "+value) {
		t.Fatalf("fragment %q", body)
	}
	if h.fileContent(h.cfgPath) != mainBefore {
		t.Fatal("the main config changed")
	}
	if !strings.Contains(stdout, "db") || !strings.Contains(stdout, value) {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestCreatedFilesDoNotDependOnTheUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	h := newSetupHost(t)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.assertMode(h.path("secrets/db"), 0o600)
	h.assertMode(h.path("agent.d/secret-db.yaml"), 0o640)
	h.assertMode(h.agentD(), 0o750)
}

func TestMissingSecretsDirectoryIsCreatedForTheServiceUser(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.RemoveAll(h.secretsDir()))
	h.cfg.Repositories = nil
	h.cfg.Secrets = nil
	h.saveConfig()
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.assertOwner(h.secretsDir(), serviceUID)
	h.assertMode(h.secretsDir(), 0o700)
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryFilesLiveInTheDirectoryOfTheirTarget(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	temps := 0
	for _, e := range h.fsys.events {
		fields := strings.Fields(e)
		if fields[0] != "createtemp" {
			continue
		}
		temps++
		dir := filepath.Dir(fields[1])
		if dir != h.secretsDir() && dir != h.agentD() {
			t.Errorf("a temporary file outside the target directories: %s", fields[1])
		}
	}
	if temps != 2 {
		t.Fatalf("%d temporary files, want one per file written", temps)
	}
	for _, dir := range []string{h.secretsDir(), h.agentD()} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp-") {
				t.Errorf("%s was left in %s", e.Name(), dir)
			}
		}
	}
}

func TestRepeatingTheSameValueChangesNothing(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil
	before := h.hostTree()
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertHostUnchanged(before)
}

func TestANewValueReplacesTheOldOneAndIsApplied(t *testing.T) {
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER-1")
	h.sd.calls, h.log.lines = nil, nil
	code, stdout, _ := h.set("db", "SECRET-MARKER-2")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER-2" {
		t.Fatalf("stored %q", got)
	}
	h.assertApplied(stdout)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "secret db updated") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestASecretOfTheMainConfigIsNotChanged(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.set("pg", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitUsage, "DEFINED_IN_CONFIG")
	if !strings.Contains(stderr, h.cfgPath) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestAFileAnotherKeyRefersToIsNotOverwritten(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories[0].PasswordFile = h.path("secrets/db")
	h.saveConfig()
	h.write(h.path("secrets/db"), "the password", 0o600)
	before := h.hostTree()
	code, _, stderr := h.set("db", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitUsage, "PATH_IN_USE")
	if !strings.Contains(stderr, "password_file") || !strings.Contains(stderr, "base") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestALeftoverFileInPlaceOfTheValueFileIsReplaced(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("secrets/db"), "left by an interrupted command", 0o600)
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER" {
		t.Fatalf("stored %q", got)
	}
}

func TestInvalidNamesAreRefused(t *testing.T) {
	for _, name := range []string{"", "-db", "db.pass", "../db", "d b", "a" + strings.Repeat("0", 63) + "x"} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.set(name, "SECRET-MARKER")
		assertRefusal(t, code, stderr, exitUsage, "NAME_INVALID")
		h.assertHostUnchanged(before)
	}
}

func TestAName64CharactersLongIsAccepted(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.set(strings.Repeat("a", 64), "SECRET-MARKER")
	assertCode(t, code, exitOK)
}

func TestAFailedWriteLeavesNoTracesAndTheOldValue(t *testing.T) {
	for _, op := range []string{"chown", "rename", "sync", "createtemp"} {
		t.Run(op, func(t *testing.T) {
			h := newSetupHost(t)
			h.set("db", "SECRET-MARKER-1")
			h.sd.calls, h.log.lines = nil, nil
			h.fsys.failOn, h.fsys.failPath = op, h.secretsDir()
			code, _, stderr := h.set("db", "SECRET-MARKER-2")
			assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
			if !strings.Contains(stderr, h.path("secrets/db")) && op != "createtemp" {
				t.Fatalf("stderr %q", stderr)
			}
			if got := h.fileContent(h.path("secrets/db")); got != "SECRET-MARKER-1" {
				t.Fatalf("stored %q", got)
			}
			for _, dir := range []string{h.secretsDir(), h.agentD()} {
				entries, _ := os.ReadDir(dir)
				for _, e := range entries {
					if strings.Contains(e.Name(), ".tmp-") {
						t.Errorf("%s left in %s", e.Name(), dir)
					}
				}
			}
			if h.sd.touched() {
				t.Fatalf("systemctl: %v", h.sd.calls)
			}
		})
	}
}

func TestTheFragmentDirectoryThatCannotBeCreatedIsAWriteError(t *testing.T) {
	h := newSetupHost(t)
	h.fsys.failOn, h.fsys.failPath = "mkdir", "agent.d"
	before := h.hostTree()
	code, _, stderr := h.set("db", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	if !strings.Contains(stderr, h.agentD()) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestTheValueIsNeverInTheFragmentOrTheAudit(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.assertValuesHidden(stdout, stderr)
	if bytes.Contains([]byte(h.fileContent(h.path("agent.d/secret-db.yaml"))), []byte("SECRET")) {
		t.Fatal("the fragment holds the value")
	}
}

// Правило "Изменяет хост только sudo": отказы изменяющих команд

func TestChangingCommandsFromTheServiceUserAreRefusedWithTheSameCommand(t *testing.T) {
	for _, args := range [][]string{
		{"secret", "set", "db", "--stdin", "--config", "C"},
		{"secret", "remove", "pg", "--config", "C"},
		{"repo", "add", "extra", "D/repo-extra", "--config", "C"},
		{"repo", "remove", "base", "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.asService(args...)
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		for _, want := range []string{"sudo sard-agent " + args[0] + " " + args[1], args[2]} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v: stderr lacks %q: %s", args, want, stderr)
			}
		}
		h.assertHostUnchanged(before)
	}
}

func TestEveryCommandFromAnotherUserIsRefused(t *testing.T) {
	for _, args := range [][]string{
		{"secret", "list", "--config", "C"},
		{"secret", "set", "db", "--stdin", "--config", "C"},
		{"repo", "show", "base", "--config", "C"},
		{"repo", "add", "extra", "D/repo-extra", "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.asOther(args...)
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		if !strings.Contains(stderr, "sudo") {
			t.Errorf("%v: stderr %q", args, stderr)
		}
		h.assertHostUnchanged(before)
	}
}

func TestTheRightToRunIsCheckedBeforeTheConfigOfASecretCommand(t *testing.T) {
	h := newSetupHost(t)
	h.stdinIs("x")
	code, _, stderr := h.asService("secret", "set", "db", "--stdin", "--config", h.path("absent.yaml"))
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
}

func TestWithoutTheServiceUserOnTheHostASecretIsNotSet(t *testing.T) {
	h := newSetupHost(t)
	h.people = nil
	before := h.hostTree()
	code, _, stderr := h.set("db", "SECRET-MARKER")
	assertRefusal(t, code, stderr, exitUsage, "SERVICE_USER_UNKNOWN")
	if !strings.Contains(stderr, "sard-agent") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestTheServiceUserOfTheConfigOwnsTheFiles(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Service.User = "backup"
	h.saveConfig()
	code, _, _ := h.set("db", "SECRET-MARKER")
	assertCode(t, code, exitOK)
	h.assertOwner(h.path("secrets/db"), backupUID)
	if o, _ := h.fsys.ownerOf(h.path("agent.d/secret-db.yaml")); o != (ownerRec{0, backupUID}) {
		t.Fatalf("fragment owner %+v", o)
	}
}
