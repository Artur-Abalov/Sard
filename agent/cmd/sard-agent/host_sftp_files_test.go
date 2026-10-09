// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Rule "Root пишет в каталог ssh пользователя службы только через удерживаемые дескрипторы и не идёт по ссылкам".

func (h *sftpHostWorld) withUmask(mask int) {
	h.t.Helper()
	old := syscall.Umask(mask)
	h.t.Cleanup(func() { syscall.Umask(old) })
}

func (h *sftpHostWorld) setHome(path string) {
	h.t.Helper()
	service := h.people[0]
	service.Home = path
	h.people[0] = service
}

func TestAMissingSSHDirectoryIsMadeForTheServiceUserThroughTheHeldHome(t *testing.T) {
	h := newSFTPHost(t)
	h.withUmask(0)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	h.assertOwner(h.sshDir(), serviceUID)
	h.assertMode(h.sshDir(), 0o700)
	if calls := h.fsys.pathCallsIn(h.homeDir()); len(calls) != 0 {
		t.Fatalf("operations on full paths in H: %v", calls)
	}
}

func TestAMissingHomeIsMadeWhenItsParentExists(t *testing.T) {
	h := newSFTPHost(t)
	h.setHome(h.path("state"))
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	h.assertOwner(h.path("state"), serviceUID)
	h.assertMode(h.path("state"), 0o700)
	h.assertOwner(h.path("state/.ssh"), serviceUID)
	h.assertMode(h.path("state/.ssh"), 0o700)
}

func TestWithoutTheParentOfTheHomeNothingIsMadeAndTheMissingDirectoryIsNamed(t *testing.T) {
	h := newSFTPHost(t)
	h.setHome(h.path("var/lib/sard-agent"))
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	if !strings.Contains(stderr, h.path("var")) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertAbsent(h.path("var"))
	if len(h.ssh.called("ssh-keygen")) != 0 {
		t.Fatal("ssh-keygen ran")
	}
	h.assertSSHUnchanged(before)
}

func TestAnUnusableHomeOfThePasswdIsRefusedAndTheHostIsNotChanged(t *testing.T) {
	for _, home := range []string{"/", "relative", ""} {
		h := newSFTPHost(t)
		h.setHome(home)
		before := h.sshTree()
		code, _, stderr := h.sftpCmd()
		assertRefusal(t, code, stderr, exitUsage, "SSH_HOME_INVALID")
		h.assertNothingChanged(before)
	}
}

func TestAnExistingHomeAndSSHDirectoryKeepTheirOwnerAndMode(t *testing.T) {
	h := newSFTPHost(t)
	h.fsys.setOwner(h.homeDir(), 0, 0)
	h.keyAndHostKnown()
	ok(t, os.Chmod(h.sshDir(), 0o750))
	h.fsys.setOwner(h.sshDir(), serviceUID, serviceUID)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	if o, _ := h.fsys.ownerOf(h.homeDir()); o.uid != 0 {
		t.Errorf("owner of H %+v", o)
	}
	h.assertOwner(h.sshDir(), serviceUID)
	h.assertMode(h.sshDir(), 0o750)
	h.assertMode(h.homeDir(), 0o755)
}

func TestALinkOnTheWayToTheSSHDirectoryIsRefusedAndNothingOutsideChanges(t *testing.T) {
	for name, plant := range map[string]struct {
		prepare func(h *sftpHostWorld, outside string)
		reason  string
		part    func(h *sftpHostWorld) string
	}{
		"the ssh directory is a link": {func(h *sftpHostWorld, outside string) { ok(h.t, os.Symlink(outside, h.sshDir())) }, "SSH_FILE_REJECTED",
			func(h *sftpHostWorld) string { return h.sshDir() }},
		"the home is behind a link": {func(h *sftpHostWorld, outside string) {
			ok(h.t, os.Symlink(outside, h.path("link")))
			h.setHome(h.path("link/home"))
		}, "SSH_HOME_INVALID", func(h *sftpHostWorld) string { return h.path("link") }},
		"the home is a link": {func(h *sftpHostWorld, outside string) {
			ok(h.t, os.Symlink(outside, h.path("hlink")))
			h.setHome(h.path("hlink"))
		}, "SSH_HOME_INVALID", func(h *sftpHostWorld) string { return h.path("hlink") }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newSFTPHost(t)
			outside := t.TempDir()
			ok(t, os.WriteFile(filepath.Join(outside, "root-file"), []byte("root\n"), 0o600))
			ok(t, os.Mkdir(filepath.Join(outside, "home"), 0o755))
			plant.prepare(h, outside)
			otherBefore, err := tree(outside, h.fsys)
			ok(t, err)
			code, _, stderr := h.sftpCmd()
			assertRefusal(t, code, stderr, exitUsage, plant.reason)
			if !strings.Contains(stderr, plant.part(h)) {
				t.Errorf("stderr lacks %s:\n%s", plant.part(h), stderr)
			}
			otherAfter, err := tree(outside, h.fsys)
			ok(t, err)
			if d := diff(otherBefore, otherAfter); len(d) != 0 {
				t.Errorf("outside changed: %v", d)
			}
			for _, e := range h.fsys.events {
				if strings.Contains(e, outside) {
					t.Errorf("an operation reached the outside: %s", e)
				}
			}
			if len(h.ssh.called("ssh-keygen")) != 0 {
				t.Error("ssh-keygen ran")
			}
			h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
		})
	}
}

func TestADirectorySwappedForALinkAfterItWasOpenedDoesNotTakeTheWriteAway(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("id_ed25519", "PRIVATE-KEY-MARKER", 0o600)
	h.putSSH("id_ed25519.pub", "ssh-ed25519 AAAAexistingkey x\n", 0o644)
	outside := t.TempDir()
	h.srv.onScan = func() {
		ok(t, os.Rename(h.sshDir(), filepath.Join(h.homeDir(), "moved")))
		ok(t, os.Symlink(outside, h.sshDir()))
	}
	code, _, stderr := h.sftpCmd()
	_ = stderr
	_ = code
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was made outside: %v", entries)
	}
	if calls := h.fsys.pathCallsIn(h.homeDir()); len(calls) != 0 {
		t.Fatalf("operations on full paths: %v", calls)
	}
	if _, err := os.Stat(filepath.Join(h.homeDir(), "moved", "known_hosts")); err != nil {
		t.Fatalf("the write did not land in the directory held: %v", err)
	}
}

func TestAPlantedFileOfTheSSHDirectoryIsRefusedAndNotReadFurther(t *testing.T) {
	victim := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "victim")
		ok(t, os.WriteFile(path, []byte("root-secret\n"), 0o600))
		return path
	}
	for name, c := range map[string]struct {
		file  string
		plant func(h *sftpHostWorld, victim string)
	}{
		"known_hosts is a link":      {"known_hosts", func(h *sftpHostWorld, v string) { ok(h.t, os.Symlink(v, h.sshFile("known_hosts"))) }},
		"known_hosts is a hard link": {"known_hosts", func(h *sftpHostWorld, v string) { ok(h.t, os.Link(v, h.sshFile("known_hosts"))) }},
		"known_hosts is a directory": {"known_hosts", func(h *sftpHostWorld, _ string) { ok(h.t, os.Mkdir(h.sshFile("known_hosts"), 0o700)) }},
		"known_hosts is a FIFO":      {"known_hosts", func(h *sftpHostWorld, _ string) { ok(h.t, syscall.Mkfifo(h.sshFile("known_hosts"), 0o600)) }},
		"config is a link":           {"config", func(h *sftpHostWorld, v string) { ok(h.t, os.Symlink(v, h.sshFile("config"))) }},
		"config is a hard link":      {"config", func(h *sftpHostWorld, v string) { ok(h.t, os.Link(v, h.sshFile("config"))) }},
		"config belongs to uid 1000": {"config", func(h *sftpHostWorld, _ string) {
			h.putSSH("config", "Host *\n", 0o644)
			h.fsys.setOwner(h.sshFile("config"), aliceUID, aliceUID)
		}},
		"the public key is a link":           {"id_ed25519.pub", func(h *sftpHostWorld, v string) { ok(h.t, os.Symlink(v, h.sshFile("id_ed25519.pub"))) }},
		"the private key is a dangling link": {"id_ed25519", func(h *sftpHostWorld, v string) { ok(h.t, os.Symlink(v+".none", h.sshFile("id_ed25519"))) }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newSFTPHost(t)
			ok(t, os.MkdirAll(h.sshDir(), 0o700))
			v := victim(t)
			c.plant(h, v)
			done := make(chan cmdResult, 1)
			go func() {
				code, stdout, stderr := h.sftpCmd()
				done <- cmdResult{code, stdout, stderr}
			}()
			r := within(t, done)
			assertRefusal(t, r.code, r.stderr, exitUsage, "SSH_FILE_REJECTED")
			if !strings.Contains(r.stderr, h.sshFile(c.file)) {
				t.Errorf("stderr lacks the path:\n%s", r.stderr)
			}
			if data, _ := os.ReadFile(v); string(data) != "root-secret\n" {
				t.Errorf("the victim changed: %q", data)
			}
			if len(h.ssh.called("ssh-keygen")) != 0 {
				t.Error("ssh-keygen ran")
			}
			h.assertNoSFTPTraces()
		})
	}
}

func TestAFileOfTheSSHDirectoryLargerThanOneMiBIsRefusedAndOneOfExactlyOneMiBIsAccepted(t *testing.T) {
	padding := func(n int) string {
		var b strings.Builder
		for b.Len() < n-1 {
			b.WriteString("#")
		}
		return b.String() + "\n"
	}
	h := newSFTPHost(t)
	h.putSSH("known_hosts", padding(1<<20+1), 0o600)
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_FILE_REJECTED")
	if !strings.Contains(stderr, "1048576") || !strings.Contains(stderr, h.sshFile("known_hosts")) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertSSHUnchanged(before)

	h = newSFTPHost(t)
	h.putSSH("known_hosts", padding(1<<20), 0o600)
	code, _, stderr = h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
}

func TestAFileOfRootWithOneNameIsAcceptedAndBelongsToTheServiceUserAfterwards(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("config", "Host *\n    Compression yes\n", 0o644)
	h.fsys.setOwner(h.sshFile("config"), 0, 0)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	h.assertOwner(h.sshFile("config"), serviceUID)
	h.assertMode(h.sshFile("config"), 0o600)
	if got := h.fileContent(h.sshFile("config")); !strings.HasSuffix(got, "Host *\n    Compression yes\n") {
		t.Fatalf("config %q", got)
	}
}

func TestAFileOfTheSSHDirectoryIsWrittenToATemporaryFileWithItsOwnerBeforeItsContentAndRenamedInTheSameDirectory(t *testing.T) {
	h := newSFTPHost(t)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	if calls := h.fsys.pathCallsIn(h.homeDir()); len(calls) != 0 {
		t.Fatalf("operations on full paths in H: %v", calls)
	}
	for _, name := range []string{"known_hosts", "config"} {
		created, owned, renamed := h.writeSteps(name)
		if created < 0 || owned < created || renamed < owned {
			t.Errorf("%s: created %d, owned %d, renamed %d in %v", name, created, owned, renamed, h.fsys.events)
		}
		h.assertOwner(h.sshFile(name), serviceUID)
	}
}

// writeSteps are the positions in the events of the creation of the
// temporary file of name, its owner change, and its rename.
func (h *sftpHostWorld) writeSteps(name string) (created, owned, renamed int) {
	tmp := h.sshFile("." + name + ".tmp-")
	created, owned, renamed = -1, -1, -1
	for i, e := range h.fsys.events {
		switch {
		case strings.HasPrefix(e, "dir-create "+tmp):
			created = i
		case strings.HasPrefix(e, "chown "+tmp) && strings.HasSuffix(e, " 990:990"):
			owned = i
		case strings.HasPrefix(e, "dir-rename "+tmp) && strings.HasSuffix(e, " "+h.sshFile(name)):
			renamed = i
		}
	}
	return created, owned, renamed
}

func TestTheModesOfTheSSHFilesDoNotDependOnTheUmask(t *testing.T) {
	h := newSFTPHost(t)
	h.withUmask(0)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	h.assertMode(h.sshFile("known_hosts"), 0o600)
	h.assertMode(h.sshFile("config"), 0o600)
}

func TestRootNeverOpensThePrivateKey(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	for _, opened := range h.fsys.opened {
		if strings.HasSuffix(opened, "/id_ed25519") {
			t.Fatalf("the private key was opened: %v", h.fsys.opened)
		}
	}
	for _, c := range h.fsys.byPath {
		if strings.Contains(c, "id_ed25519") {
			t.Fatalf("a call by path: %v", h.fsys.byPath)
		}
	}
}

func TestAFailedWriteToTheSSHDirectoryIsAWriteErrorAndTheOldContentIsWhole(t *testing.T) {
	for _, step := range []string{"chown", "rename", "sync"} {
		h := newSFTPHost(t)
		h.putSSH("known_hosts", keyRSA.line("other.example.com"), 0o600)
		h.putSSH("id_ed25519", "PRIVATE-KEY-MARKER", 0o600)
		h.putSSH("id_ed25519.pub", "ssh-ed25519 AAAAexistingkey x\n", 0o644)
		h.fsys.failOn, h.fsys.failPath = step, "known_hosts"
		code, _, stderr := h.sftpCmd()
		assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
		if !strings.Contains(stderr, h.sshFile("known_hosts")) {
			t.Errorf("%s: stderr %q", step, stderr)
		}
		if got := h.fileContent(h.sshFile("known_hosts")); got != keyRSA.line("other.example.com") {
			t.Errorf("%s: known_hosts %q", step, got)
		}
		h.assertNoSFTPTraces()
	}
}

func TestAHomeOfAnotherUserOrOpenToOthersIsRefusedAndTheHostIsNotChanged(t *testing.T) {
	h := newSFTPHost(t)
	h.fsys.setOwner(h.homeDir(), aliceUID, aliceUID)
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_HOME_INVALID")
	h.assertNothingChanged(before)

	h = newSFTPHost(t)
	ok(t, os.Chmod(h.homeDir(), 0o777))
	code, _, stderr = h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_HOME_INVALID")
	h.assertNoSSHProgram()
}

func TestAKnownHostsOthersCanWriteIsRefusedAndNeverTrusted(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyED.line(sftpHost), 0o600)
	ok(t, os.Chmod(h.sshFile("known_hosts"), 0o666))
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_FILE_REJECTED")
	h.assertNoSSHProgram()
}
