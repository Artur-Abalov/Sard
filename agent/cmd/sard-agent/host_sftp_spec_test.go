// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// The amendments П20–П28 of docs/specs/agent/host-setup.feature (SFTP).

// П28
func TestScanWithoutKeysIsATemporaryErrorWhateverTheExitCodeOfSSHKeyscan(t *testing.T) {
	for _, code := range []int{0, 1} {
		h := newSFTPHost(t)
		h.srv.keys, h.srv.keyscanCode = nil, code
		before := h.sshTree()
		cmdCode, _, stderr := h.sftpCmd()
		assertRefusal(t, cmdCode, stderr, exitTemporary, "BACKEND_UNAVAILABLE")
		for _, want := range []string{sftpHost, "22"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("code %d: stderr lacks %q", code, want)
			}
		}
		h.assertNothingChanged(before)
	}
}

// П27
func TestTheTextOfABannerOfTheServerDoesNotDecideTheClassOfARefusedLogin(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.srv.loginCode = 255
	h.srv.loginStderr = "NOTICE Host key verification failed. ssh: connect to host x port 22: Connection refused\nbackup@nas.example.com: Permission denied (publickey)."
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "SSH_KEY_NOT_AUTHORIZED")
	for _, not := range []string{"HOST_KEY_MISMATCH", "BACKEND_UNAVAILABLE"} {
		if strings.Contains(stderr, not) {
			t.Errorf("stderr names %s:\n%s", not, stderr)
		}
	}
}

// П27
func TestABannerWithoutALineOfSSHGivesBackendRefusedNotTheClassOfTheBanner(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.srv.loginCode = 255
	h.srv.loginStderr = "Permission denied (publickey) — call the admin"
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitAgentError, "BACKEND_REFUSED")
}

// П22
func TestReplacingTheHostKeyRemovesPlainAndHashedEntriesAndKeepsPatterns(t *testing.T) {
	h := newSFTPHost(t)
	hashed := hashedEntry(sftpHost, keyOldED)
	h.putSSH("known_hosts", keyOldED.line("*.example.com")+keyOldED.line(sftpHost)+hashed+keyRSA.line("other.example.com"), 0o600)
	code, stdout, stderr := h.sftpCmd("--replace-host-key")
	assertCode(t, code, exitOK)
	want := keyOldED.line("*.example.com") + keyRSA.line("other.example.com") + keyED.line(sftpHost)
	if got := h.fileContent(h.sshFile("known_hosts")); got != want {
		t.Fatalf("known_hosts %q, want %q (stderr %s)", got, want, stderr)
	}
	if !strings.Contains(stdout, "line 1") || !strings.Contains(stdout, h.sshFile("known_hosts")) {
		t.Fatalf("stdout does not name the kept pattern: %s", stdout)
	}
}

// П22
func TestARefusalForAChangedHostKeyNamesTheMatchingPatternEntries(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyOldED.line("*.example.com")+keyOldED.line(sftpHost), 0o600)
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitTrust, "HOST_KEY_CHANGED")
	for _, want := range []string{"line 2", "line 1", "pattern"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	h.assertNothingChanged(before)
}

// П21
func TestARefusalAfterWritingSSHFilesSaysTheyStayForARepeat(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.authorized = false
	code, stdout, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_KEY_NOT_AUTHORIZED")
	out := stdout + stderr
	for _, want := range []string{h.sshFile("id_ed25519"), h.sshFile("known_hosts"), h.sshFile("config"), "stay", "repeated"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
}

// П21
func TestARefusalBeforeWritingSSHFilesPrintsNoNoteAboutThem(t *testing.T) {
	h := newSFTPHost(t)
	h.stdin.Reset()
	code, stdout, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "HOST_KEY_UNCONFIRMED")
	if strings.Contains(stdout+stderr, "stay") {
		t.Fatalf("a note about files that were not written:\n%s", stderr)
	}
}

// П20
func TestRepeatingAnSFTPCommandChecksTheLoginBeforeUnchanged(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	h.srv.authorized = false
	before := h.hostTree()
	code, stdout, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "SSH_KEY_NOT_AUTHORIZED")
	if strings.Contains(stdout, "unchanged") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertSSHUnchanged(sshOnly(before, h.homeDir()))
}

// sshOnly is the part of a tree under dir.
func sshOnly(tree map[string]string, dir string) map[string]string {
	got := map[string]string{}
	for p, v := range tree {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			got[p] = v
		}
	}
	return got
}

// П23
func TestThePublicKeyIsReadAtMode0644AndRefusedAtMode0664(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, _, stderr := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	_ = stderr

	h = newSFTPHost(t)
	h.keyAndHostKnown()
	ok(t, os.Chmod(h.sshFile("id_ed25519.pub"), 0o664))
	code, _, stderr = h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "SSH_FILE_REJECTED")
	if !strings.Contains(stderr, h.sshFile("id_ed25519.pub")) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertNoSSHProgram()
}

// П24
func TestRepeatingAConnectedRemoteRepositoryWithAPasswordSourceIsAUsageErrorBeforeAnyRead(t *testing.T) {
	for _, c := range []struct {
		remote string
		flag   []string
		name   string
	}{
		{"sftp", []string{"--password-stdin"}, "--password-stdin"},
		{"sftp", []string{"--password-from-file", "P"}, "--password-from-file"},
		{"s3", []string{"--password-from-file", "P"}, "--password-from-file"},
	} {
		t.Run(c.remote+" "+c.name, func(t *testing.T) {
			h := newSFTPHost(t)
			run := h.connectedRepeat(c.remote)
			var opened []string
			h.deps.openFile = func(p string) (io.ReadCloser, error) { opened = append(opened, p); return os.Open(p) }
			p := h.path("outside/P")
			h.write(p, "other\n", 0o600)
			h.stdinIs("unread")
			before, calls := h.hostTree(), len(h.restic.calls)
			code, _, stderr := run(strings.Split(strings.ReplaceAll(strings.Join(c.flag, " "), "P", p), " ")...)
			assertCode(t, code, exitUsage)
			if !strings.Contains(stderr, c.name) || len(opened) != 0 || h.stdin.String() != "unread" {
				t.Errorf("stderr %q, opened %v, stdin %q", stderr, opened, h.stdin.String())
			}
			if got := h.restic.subs()[calls:]; len(got) != 1 || got[0] != "version" {
				t.Errorf("restic calls %v", got)
			}
			h.assertHostUnchanged(before)
		})
	}
}

// connectedRepeat connects the repository and returns the command that repeats it.
func (h *sftpHostWorld) connectedRepeat(remote string) func(extra ...string) (int, string, string) {
	if remote == "sftp" {
		h.connectedSFTP()
		return func(extra ...string) (int, string, string) { return h.sftpAt(sftpAddress, extra...) }
	}
	h.connectedS3()
	return func(extra ...string) (int, string, string) {
		return h.sudo(append([]string{"repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C"}, extra...)...)
	}
}

// hashedEntry is a line of known_hosts with the host written as ssh -H does.
func hashedEntry(host string, k serverKey) string {
	return fmt.Sprintf("%s %s %s\n", hashedHost(host), k.keyType, k.blob)
}
