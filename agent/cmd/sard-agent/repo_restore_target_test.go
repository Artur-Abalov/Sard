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

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "Цель восстановления проверяется до записи и не принимается молча".

func TestAMissingTargetIsCreatedForRootWithMode0700EvenWithUmask000(t *testing.T) {
	h := restoreHost(t)
	defer syscall.Umask(syscall.Umask(0))
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	h.assertOwner(h.target(), 0)
	h.assertMode(h.target(), 0o700)
	if o, _ := h.fsys.ownerOf(h.target()); o.gid != 0 {
		t.Errorf("group %d", o.gid)
	}
}

func TestMissingParentsOfTheTargetAreCreatedToo(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", h.path("a/b/c"), "--config", "C")
	assertCode(t, code, exitOK)
	for _, p := range []string{"a", "a/b", "a/b/c"} {
		h.assertOwner(h.path(p), 0)
		h.assertMode(h.path(p), 0o700)
	}
}

func TestANonEmptyTargetIsRefusedWithoutOverwrite(t *testing.T) {
	h := restoreHost(t)
	h.write(h.target()+"/keep.txt", "KEEP", 0o600)
	before := h.hostTree()
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitUsage, "TARGET_NOT_EMPTY")
	for _, want := range []string{h.target(), "--overwrite"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	if len(h.restoreCalls()) != 0 {
		t.Error("restic restore was called")
	}
	h.assertHostUnchanged(before)
}

// Н30
func TestATargetWithOnlyAnEmptyLostAndFoundIsEmpty(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target()+"/lost+found", 0o700))
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
}

func TestATargetWithAFilledLostAndFoundIsNotEmpty(t *testing.T) {
	h := restoreHost(t)
	h.write(h.target()+"/lost+found/f", "x", 0o600)
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitUsage, "TARGET_NOT_EMPTY")
}

func TestOverwriteReplacesTheFilesOfTheSnapshotAndKeepsTheRest(t *testing.T) {
	h := restoreHost(t)
	h.write(h.target()+"/srv/data/a.txt", "OLD", 0o600)
	h.write(h.target()+"/keep.txt", "KEEP", 0o600)
	code, _, _ := h.restore(idF, "--overwrite")
	assertCode(t, code, exitOK)
	args := h.restoreCalls()[0].args
	if i := slices.Index(args, "--overwrite"); i < 0 || args[i+1] != "always" {
		t.Errorf("restic args %q: the files must be replaced", args)
	}
	if got := h.fileContent(h.target() + "/srv/data/a.txt"); got != "A-DATA" {
		t.Errorf("a.txt = %q", got)
	}
	if got := h.fileContent(h.target() + "/keep.txt"); got != "KEEP" {
		t.Errorf("keep.txt = %q", got)
	}
}

func TestWithoutOverwriteResticIsToldNotToReplaceAnything(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	args := h.restoreCalls()[0].args
	if i := slices.Index(args, "--overwrite"); i < 0 || args[i+1] != "never" {
		t.Errorf("restic args %q", args)
	}
}

func TestAnUnusableTargetIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	for _, c := range []struct {
		name    string
		arrange func(h *setupHost)
		target  func(h *setupHost) string
		named   func(h *setupHost) string
	}{
		{"relative", func(*setupHost) {}, func(*setupHost) string { return "restore" }, func(*setupHost) string { return "absolute" }},
		{"below a link", linkToReal, func(h *setupHost) string { return h.path("link/r") }, func(h *setupHost) string { return h.path("link") }},
		{"a link", linkToReal, func(h *setupHost) string { return h.path("link") }, func(h *setupHost) string { return h.path("link") }},
		{"a file", plainFile, func(h *setupHost) string { return h.path("file") }, func(h *setupHost) string { return h.path("file") }},
		{"below a file", plainFile, func(h *setupHost) string { return h.path("file/r") }, func(h *setupHost) string { return h.path("file") }},
		{"below the repository", func(*setupHost) {}, func(h *setupHost) string { return h.path("base/r") }, func(h *setupHost) string { return h.path("base") }},
		{"the repository", func(*setupHost) {}, func(h *setupHost) string { return h.path("base") }, func(h *setupHost) string { return h.path("base") }},
		{"below the cache", func(h *setupHost) { h.cfg.Restic.CacheDir = h.path("K"); h.saveConfig() }, func(h *setupHost) string { return h.path("K/r") }, func(h *setupHost) string { return h.path("K") }},
	} {
		h := restoreHost(t)
		c.arrange(h)
		before := h.hostTree()
		code, _, stderr := h.sudo("repo", "restore", "base", "aaaa1111", "--target", c.target(h), "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "TARGET_INVALID")
		if !strings.Contains(stderr, c.named(h)) {
			t.Errorf("%s: stderr lacks %q: %s", c.name, c.named(h), stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
	}
}

func linkToReal(h *setupHost) {
	ok(h.t, os.Mkdir(h.path("real"), 0o700))
	ok(h.t, os.Symlink(h.path("real"), h.path("link")))
}

func plainFile(h *setupHost) { h.write(h.path("file"), "x", 0o600) }

func TestWithoutATargetTheCommandIsAUsageError(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.sudo("repo", "restore", "base", "aaaa1111", "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--target") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}

func TestTheTargetIsUsedInItsCleanForm(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", h.path(".")+"/./restore/", "--config", "C")
	assertCode(t, code, exitOK)
	if got := h.restoreCalls()[0].args; !slices.Contains(got, h.target()) {
		t.Errorf("restic args %q", got)
	}
	if !strings.Contains(stdout, "target:   "+h.target()+"\n") {
		t.Errorf("stdout %q", stdout)
	}
}

// Н31
func TestTheRootNeedsItsOwnFlag(t *testing.T) {
	for _, c := range []struct {
		target string
		flags  []string
	}{{"/", nil}, {"/", []string{"--overwrite"}}, {"/.", []string{"--overwrite"}}, {"//", []string{"--overwrite"}}} {
		h := restoreHost(t)
		code, _, stderr := h.sudo(append([]string{"repo", "restore", "base", "aaaa1111", "--target", c.target, "--config", "C"}, c.flags...)...)
		assertRefusal(t, code, stderr, exitUsage, "ROOT_TARGET_REFUSED")
		if !strings.Contains(stderr, "--allow-root-target") {
			t.Errorf("%q %v: stderr %q", c.target, c.flags, stderr)
		}
		h.assertNoBackendCalls()
	}
}

func TestTheRootWithoutOverwriteIsNeverEmpty(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.sudo("repo", "restore", "base", "aaaa1111", "--target", "/", "--allow-root-target", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "TARGET_NOT_EMPTY")
	if !strings.Contains(stderr, "--overwrite") || len(h.restoreCalls()) != 0 {
		t.Errorf("stderr %q, restore calls %v", stderr, h.restoreCalls())
	}
}

func TestTheRootWithBothFlagsIsRestoredOntoItsPlaces(t *testing.T) {
	h := restoreHost(t)
	code, _, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", "/", "--allow-root-target", "--overwrite", "--config", "C")
	assertCode(t, code, exitOK)
	args := h.restoreCalls()[0].args
	if i := slices.Index(args, "--target"); args[i+1] != "/" || args[slices.Index(args, "--overwrite")+1] != "always" {
		t.Errorf("restic args %q", args)
	}
	if line := h.lastAudit(); !strings.Contains(line, "repository base snapshot aaaa1111 restored to /") {
		t.Errorf("audit %q", line)
	}
}

func TestAllowRootTargetWithAnotherTargetIsAUsageError(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.restore(idF, "--allow-root-target")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--allow-root-target") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}

// Rule "Снимок каталога восстанавливается под исходными путями с владельцами из снимка".

func TestTheSummaryOfARestoredDirectory(t *testing.T) {
	h := restoreHost(t)
	h.base().restoreState.summary = &restic.RestoreSummary{Files: 2, Bytes: 2048}
	code, stdout, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	for _, want := range []string{"base", idF, "2026-10-10T03:00:00Z", "s1", "r1", h.target(), "2 files", "2048 bytes", "restored",
		"under their original paths", filepath.Join(h.target(), "srv/data")} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestASnapshotWithoutSardTagsIsRestoredAsADirectory(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.restore("dddd1111")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "source:   -\n") || !strings.Contains(stdout, "run:      -\n") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAPathLimitsTheRestoreToItself(t *testing.T) {
	h := restoreHost(t)
	code, stdout, _ := h.restore("aaaa1111", "--path", "/srv/data/sub")
	assertCode(t, code, exitOK)
	args := h.restoreCalls()[0].args
	var includes []string
	for i, a := range args {
		if a == "--include" {
			includes = append(includes, args[i+1])
		}
	}
	if !slices.Equal(includes, []string{"/srv/data/sub"}) {
		t.Errorf("includes %v", includes)
	}
	if !strings.Contains(stdout, "/srv/data/sub") {
		t.Errorf("stdout %q", stdout)
	}
	h.assertAbsent(h.target() + "/srv/data/a.txt")
	if got := h.fileContent(h.target() + "/srv/data/sub/b.txt"); got != "B-DATA" {
		t.Errorf("b.txt = %q", got)
	}
}

func TestAPathThatIsNotInTheSnapshotIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, stderr := h.restore("aaaa1111", "--path", "/srv/nothing")
	assertRefusal(t, code, stderr, exitUsage, "PATH_NOT_IN_SNAPSHOT")
	if !strings.Contains(stderr, "/srv/nothing") || len(h.restoreCalls()) != 0 {
		t.Errorf("stderr %q, restore calls %v", stderr, h.restoreCalls())
	}
	h.assertHostUnchanged(before)
}

func TestAPathThatIsNotLiteralIsAUsageErrorNamingTheFlag(t *testing.T) {
	for _, p := range []string{"srv/data", "/srv/*", "/srv/da?a", "/srv/[d]ata", "/srv/data/../etc", ""} {
		h := restoreHost(t)
		code, _, stderr := h.restore("aaaa1111", "--path", p)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--path") {
			t.Errorf("%q: stderr %q", p, stderr)
		}
		h.assertNoBackendCalls()
	}
}
