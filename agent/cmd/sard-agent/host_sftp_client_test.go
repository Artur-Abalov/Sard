// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rule "Без клиента OpenSSH подключение SFTP понятно отказывает".

func TestWithoutAProgramOfTheOpenSSHClientTheConnectionIsRefusedBeforeAnyChange(t *testing.T) {
	for _, program := range []string{"ssh", "sftp", "ssh-keygen", "ssh-keyscan"} {
		h := newSFTPHost(t)
		ok(t, os.Remove(filepath.Join(h.programsDir(), program)))
		before := h.hostTree()
		code, stdout, stderr := h.sftpCmd()
		assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_MISSING")
		for _, want := range []string{program, "sudo apt-get install openssh-client", "sudo dnf install openssh-clients", "dpkg -i"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q:\n%s", program, want, stderr)
			}
		}
		h.assertNoBackendCalls()
		h.assertNoSSHProgram()
		h.assertHostUnchanged(before)
		h.assertSFTPValuesHidden(stdout, stderr)
	}
}

func TestAProgramThatIsNotInThePathOfResticIsMissing(t *testing.T) {
	h := newSFTPHost(t)
	elsewhere := h.path("elsewhere")
	h.write(filepath.Join(elsewhere, "ssh"), "#!/bin/sh\n", 0o755)
	ok(t, os.Remove(filepath.Join(h.programsDir(), "ssh")))
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_MISSING")
	if !strings.Contains(stderr, "ssh") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestAProgramWithoutTheExecuteBitIsMissingAndADirectoryIsNoProgram(t *testing.T) {
	h := newSFTPHost(t)
	ok(t, os.Chmod(filepath.Join(h.programsDir(), "sftp"), 0o644))
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_MISSING")
	h = newSFTPHost(t)
	ok(t, os.Remove(filepath.Join(h.programsDir(), "ssh-keygen")))
	ok(t, os.Mkdir(filepath.Join(h.programsDir(), "ssh-keygen"), 0o755))
	code, _, stderr = h.sftpCmd()
	assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_MISSING")
}

func TestAnS3ConnectionNeedsNoOpenSSHClient(t *testing.T) {
	h := newSFTPHost(t)
	h.deps.pathEnv = h.path("empty")
	code, _, stderr := h.s3Cmd()
	assertCode(t, code, exitOK)
	_ = stderr
	if !h.ssh.none() {
		t.Fatalf("the client ran: %+v", h.ssh.calls)
	}
}

func TestAnUnforeseenFailureOfTheKeygenIsAnAgentErrorWithoutAHostKeyOnDisk(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.keygenStderr = "Saving key failed"
	code, stdout, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_FAILED")
	if !strings.Contains(stderr, "Saving key failed") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertAbsent(h.sshFile("known_hosts"), h.path("agent.d/repo-extra.yaml"))
	h.assertSFTPValuesHidden(stdout, stderr)
}

func TestADirectoryOfThePathThatIsNotAbsoluteIsNotSearched(t *testing.T) {
	h := newSFTPHost(t)
	t.Chdir(h.dir)
	h.deps.pathEnv = "bin"
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitAgentError, "SSH_CLIENT_MISSING")
}
