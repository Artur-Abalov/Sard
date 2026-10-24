// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// docs/specs/agent/restic-keys.feature, rule "Пароль нового ключа показывается ровно один раз".

func TestInATerminalThePasswordIsPrintedOnceWithAWarning(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	password := h.added().password
	if strings.Count(stdout, password) != 1 || !strings.Contains(stdout, "this password is stored nowhere else") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h.assertPasswordHidden(password, stdout, stderr)
}

func TestAfterTheTerminalShowThereIsNoFileWithThePasswordOnTheHost(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitOK)
	if found := h.filesHolding(h.added().password); len(found) != 0 {
		t.Errorf("files hold the password: %v", found)
	}
}

func TestAfterAResticRefusalThereIsNoFileWithThePasswordOnTheHost(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.base().keyring.fail = map[string]string{"add": "Fatal: something went wrong"}
	code, _, _ := h.keyCmd("add", "base", "--for", "recovery")
	assertCode(t, code, exitAgentError)
	if found := h.filesHolding(h.base().lastAddedOrCall().password); len(found) != 0 {
		t.Errorf("files hold the password: %v", found)
	}
}

func TestWithoutATerminalAndWithoutAFileNoKeyIsIssued(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	h.deps.stdoutTerminal = false
	code, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertRefusal(t, code, stderr, exitUsage, "KEY_OUTPUT_MISSING")
	assertReason(t, stderr, "--to-file")
	h.assertNoResticCalls()
}

// Not parallel: the umask is the process', and a parallel test would create its directories under 0.
func TestThePasswordGoesToAFileWithMode0600OwnedByRootWhateverTheUmask(t *testing.T) {
	h := newIssueHost(t)
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	file := h.path("out/vk")
	ok(t, os.MkdirAll(filepath.Dir(file), 0o755))
	code, stdout, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", file)
	assertCode(t, code, exitOK)
	password := h.added().password
	if got := h.fileContent(file); got != password+"\n" {
		t.Errorf("file holds %q", got)
	}
	h.assertOwner(file, 0)
	h.assertMode(file, 0o600)
	if strings.Contains(stdout, password) {
		t.Errorf("stdout holds the password:\n%s", stdout)
	}
	h.assertPasswordHidden(password, stdout, stderr)
}

func TestWithAFileAndATerminalThePasswordGoesOnlyToTheFile(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	file := h.path("out/vk")
	ok(t, os.MkdirAll(filepath.Dir(file), 0o755))
	_, stdout, _ := h.keyCmd("add", "base", "--for", "verifier", "--to-file", file)
	if strings.Contains(stdout, h.added().password) || !strings.Contains(stdout, file) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// tempFileOf is the temporary file the events of the fake file system say was
// made for a file called name, "" if none.
func (h *keysHost) tempFileOf(name string) string {
	for _, e := range h.fsys.events {
		if path, found := strings.CutPrefix(e, "createtemp "); found && strings.Contains(path, name) {
			return path
		}
	}
	return ""
}

// eventIndex is the position of the first event that starts with the text, -1 if none.
func (h *keysHost) eventIndex(prefix string) int {
	return slices.IndexFunc(h.fsys.events, func(e string) bool { return strings.HasPrefix(e, prefix) })
}

func TestThePasswordFileIsWrittenAtomicallyInTheDestinationDirectory(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	dir := h.path("out")
	ok(t, os.MkdirAll(dir, 0o755))
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier", "--to-file", dir+"/vk")
	assertCode(t, code, exitOK)
	temp := h.tempFileOf("vk")
	if filepath.Dir(temp) != dir {
		t.Errorf("the temporary file %q is not in %s", temp, dir)
	}
	if left := h.tempFilesIn(dir); len(left) != 0 {
		t.Errorf("temporary files: %v", left)
	}
	if created, chowned := h.eventIndex("createtemp "+temp), h.eventIndex("chown "+temp); created < 0 || chowned < created {
		t.Errorf("the owner of the temporary file was set at %d, it was created at %d", chowned, created)
	}
}

func TestAnExistingFileIsNotOverwritten(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	file := h.path("out/vk")
	h.write(file, "mine", 0o600)
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", file)
	assertRefusal(t, code, stderr, exitUsage, "KEY_FILE_EXISTS")
	if h.fileContent(file) != "mine" {
		t.Error("the file changed")
	}
	h.assertNoResticCalls()
}

func TestAMissingDirectoryOfThePasswordFileIsFoundBeforeRestic(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", h.path("missing/vk"))
	assertRefusal(t, code, stderr, exitWrite, "KEY_FILE_WRITE")
	assertReason(t, stderr, h.path("missing"))
	h.assertNoResticCalls()
}

func TestARelativePasswordFilePathIsRefused(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", "vk")
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "--to-file")
	h.assertNoResticCalls()
}

func TestASymbolicLinkInThePathOfThePasswordFileIsRefused(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	ok(t, os.Symlink(h.path("out"), h.path("link")))
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", h.path("link/vk"))
	assertRefusal(t, code, stderr, exitUsage, "KEY_FILE_REJECTED")
	assertReason(t, stderr, h.path("link"))
	if entries, _ := os.ReadDir(h.path("out")); len(entries) != 0 {
		t.Errorf("new files in out: %v", entries)
	}
	h.assertNoResticCalls()
}

func TestAResticRefusalLeavesNoPasswordFile(t *testing.T) {
	t.Parallel()
	h := newIssueHost(t)
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	h.base().keyring.fail = map[string]string{"add": "Fatal: something went wrong"}
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier", "--to-file", h.path("out/vk"))
	assertCode(t, code, exitAgentError)
	h.assertAbsent(h.path("out/vk"))
	if left := h.tempFilesIn(h.path("out")); len(left) != 0 {
		t.Errorf("temporary files: %v", left)
	}
}
