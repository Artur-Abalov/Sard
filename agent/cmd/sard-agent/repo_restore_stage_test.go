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

// Rule "restic под root пишет только во временный каталог root внутри цели,
// затем записи переносятся в цель" (Р73).

// stagingLeft are the temporary directories in the target.
func (h *setupHost) stagingLeft() []string {
	left, _ := filepath.Glob(filepath.Join(h.target(), ".sard-restore-*"))
	return left
}

func (h *setupHost) assertNoStaging() {
	h.t.Helper()
	if left := h.stagingLeft(); len(left) != 0 {
		h.t.Errorf("temporary directories left: %v", left)
	}
}

// stagingSeen is what a temporary directory looked like when restic started.
type stagingSeen struct {
	dirs, subdir []string
	mode         os.FileMode
}

// watchStaging records the temporary directory of the target as restic starts.
func (h *setupHost) watchStaging() *stagingSeen {
	seen := &stagingSeen{}
	h.base().restoreState.before = func(string) {
		seen.dirs = h.stagingLeft()
		if len(seen.dirs) != 1 {
			return
		}
		info, err := os.Stat(seen.dirs[0])
		ok(h.t, err)
		seen.mode = info.Mode().Perm()
		seen.subdir, _ = filepath.Glob(seen.dirs[0] + "/root")
	}
	return seen
}

func TestTheTemporaryDirectoryOfRootExistsBeforeResticStarts(t *testing.T) {
	h := restoreHost(t)
	seen := h.watchStaging()
	code, _, stderr := h.restore(idF)
	if code != exitOK {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if len(seen.dirs) != 1 || seen.mode != 0o700 || len(seen.subdir) != 1 {
		t.Fatalf("before restic ran: %+v", seen)
	}
	if o, found := h.fsys.ownerOf(seen.dirs[0]); !found || o.uid != 0 || o.gid != 0 {
		t.Errorf("owner of the temporary directory: %+v (%v)", o, found)
	}
}

func TestResticGetsTheTemporaryDirectoryByDescriptorNotThePathOfTheTarget(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	c := h.restoreCalls()[0]
	i := slices.Index(c.args, "--target")
	if i < 0 || c.args[i+1] != "/proc/self/fd/3/root" {
		t.Errorf("restic args %q: the target is the root directory of descriptor 3", c.args)
	}
	for _, a := range c.args {
		if strings.Contains(a, h.target()) {
			t.Errorf("the path of the target is in the arguments: %q", a)
		}
	}
	if len(c.extra) == 0 || c.extra[0] != "dir" {
		t.Errorf("inherited %v: descriptor 3 is the staging directory", c.extra)
	}
}

func TestAfterSuccessTheEntriesAreInTheTargetAndTheTemporaryDirectoryIsGone(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore(idF)
	if code != exitOK {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if got := h.fileContent(h.target() + "/srv/data/a.txt"); got != "A-DATA" {
		t.Errorf("a.txt = %q", got)
	}
	if got := h.fileContent(h.target() + "/srv/data/sub/b.txt"); got != "B-DATA" {
		t.Errorf("b.txt = %q", got)
	}
	h.assertNoStaging()
	if n := h.fsys.countEvents("dir-renamenoreplace "); n != 1 {
		t.Errorf("%d moves without replacing, want one for srv: %v", n, h.fsys.events)
	}
}

func TestANameThatTurnsUpInTheTargetWhileResticWorksIsNotReplaced(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.before = func(string) {
		h.write(h.target()+"/srv/theirs.txt", "THEIRS", 0o600)
	}
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if !strings.Contains(stderr, h.target()+"/srv") {
		t.Errorf("stderr %q", stderr)
	}
	if got := h.fileContent(h.target() + "/srv/theirs.txt"); got != "THEIRS" {
		t.Errorf("their file: %q", got)
	}
	h.assertAbsent(h.target() + "/srv/data")
	h.assertNoStaging()
	if line := h.lastAudit(); !strings.Contains(line, "restore to "+h.target()+" failed: TARGET_WRITE") {
		t.Errorf("audit %q", line)
	}
}

func TestAMountPointFoundWhenTheEntriesAreMovedIsAnInvalidTarget(t *testing.T) {
	h := restoreHost(t)
	h.fsys.crossDevice = "srv"
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitUsage, "TARGET_INVALID")
	if !strings.Contains(stderr, "mount point inside the target") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoStaging()
}

func TestTheTemporaryDirectoryIsRemovedWhenResticFails(t *testing.T) {
	outside := ""
	for name, arrange := range map[string]func(h *setupHost){
		"the storage is lost": func(h *setupHost) { h.base().restoreState.fatal = "Fatal: dial tcp: connection refused" },
		"files not restored":  func(h *setupHost) { h.base().restoreState.items = 2 },
		"a link in it": func(h *setupHost) {
			h.base().restoreState.fatal = "Fatal: dial tcp: connection refused"
			h.base().restoreState.before = func(root string) {
				ok(t, os.MkdirAll(root, 0o700))
				ok(t, os.Symlink(outside, root+"/link"))
			}
		},
	} {
		h := restoreHost(t)
		outside = h.path("outside")
		h.write(outside+"/precious", "P", 0o600)
		arrange(h)
		h.restore(idF)
		if left := h.stagingLeft(); len(left) != 0 {
			t.Errorf("%s: left %v", name, left)
		}
		if got := h.fileContent(outside + "/precious"); got != "P" {
			t.Errorf("%s: what the link pointed at: %q", name, got)
		}
	}
}

func TestFilesRestoredBeforeSomeCouldNotBeStayInTheTarget(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.items = 1
	h.base().restoreState.before = func(root string) {
		ok(t, os.MkdirAll(root+"/srv", 0o755))
		ok(t, os.WriteFile(root+"/srv/partial", []byte("x"), 0o600))
	}
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitAgentError, "RESTORE_INCOMPLETE")
	if got := h.fileContent(h.target() + "/srv/partial"); got != "x" {
		t.Errorf("partial = %q", got)
	}
	h.assertNoStaging()
}

func TestATemporaryDirectoryOfAnotherOwnerIsNotUsed(t *testing.T) {
	h := restoreHost(t)
	h.fsys.stagingOwner = serviceUID
	before := h.hostTree()
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if len(h.restoreCalls()) != 0 {
		t.Error("restic restore was called")
	}
	h.assertNoStaging()
	h.assertAbsent(h.target())
	_ = before
}

// Р76: the file system of the target is read-only.
func TestAReadOnlyFileSystemRefusesTheTemporaryDirectoryAsATargetWriteFailure(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target(), 0o700))
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "mkdir", ".sard-restore-", syscall.EROFS
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if !strings.Contains(stderr, h.target()) || !strings.Contains(stderr, "read-only") {
		t.Errorf("stderr %q", stderr)
	}
	if len(h.restoreCalls()) != 0 {
		t.Error("restic restore was called")
	}
}

func TestResticAsRootGetsNothingToWriteButTheDescriptorsItIsGiven(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	c := h.restoreCalls()[0]
	if len(c.extra) != 2 || c.extra[0] != "dir" || c.extra[1] != "pipe" {
		t.Errorf("inherited %v: descriptor 3 the staging directory, 4 the password pipe", c.extra)
	}
	if i := slices.Index(c.args, "--password-file"); i < 0 || c.args[i+1] != "/proc/self/fd/4" {
		t.Errorf("args %q", c.args)
	}
}
