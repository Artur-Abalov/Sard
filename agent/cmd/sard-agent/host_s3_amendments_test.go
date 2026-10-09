// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// The amendments П14-П17 of docs/specs/agent/host-setup.feature.

// П14: a repeat names the key id too.
func TestRepeatingAnS3CommandWithoutAKeyIDIsAUsageErrorAndChangesNothing(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--access-key-id") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

// П14: a new region without a source of the secret key asks again; the
// env file in place is never the source of a new env file.
func TestANewRegionWithoutASecretKeySourceAsksTheSecretKeyAgain(t *testing.T) {
	h := newSetupHost(t)
	h.stdinIs(s3Marker)
	code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key-stdin", "--region", "ru-central1", "--config", "C")
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil

	t.Run("a terminal is asked twice", func(t *testing.T) {
		term := h.terminalIs("S3-MARKER-NEW", "S3-MARKER-NEW")
		code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--region", "ru-central2", "--config", "C")
		assertCode(t, code, exitOK)
		if len(term.prompts) != 2 {
			t.Fatalf("prompts %q", term.prompts)
		}
		want := "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER-NEW\nAWS_DEFAULT_REGION=ru-central2\n"
		if got := h.fileContent(h.envPath()); got != want {
			t.Fatalf("env file %q", got)
		}
	})
	t.Run("a pipe that never closes is SECRET_SOURCE_MISSING", func(t *testing.T) {
		before := h.hostTree()
		h.deps.terminal = func(io.Writer) hostsetup.Terminal { return nil }
		h.deps.stdin = &hungInput{}
		code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--region", "ru-central3", "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_MISSING")
		if h.hostTree()[h.envPath()] != before[h.envPath()] {
			t.Fatal("the env file changed")
		}
	})
}

// П16: a password source with a change of keys is refused before any
// secret is read.
func TestAPasswordSourceWithAChangeOfKeysIsRefusedBeforeAnySecretIsRead(t *testing.T) {
	for _, flag := range []string{"--password-stdin", "--password-from-file"} {
		h := newSetupHost(t)
		h.connectedS3()
		var opened []string
		h.deps.openFile = func(p string) (io.ReadCloser, error) { opened = append(opened, p); return os.Open(p) }
		src := h.path("outside/F")
		h.write(src, "key\n", 0o600)
		h.stdinIs("unread")
		args := []string{"repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-2", "--secret-key-from-file", src, "--config", "C", flag}
		if flag == "--password-from-file" {
			args = append(args, h.path("outside/P"))
		}
		before := h.hostTree()
		callsBefore := len(h.restic.calls)
		code, _, stderr := h.sudo(args...)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, flag) || !strings.Contains(stderr, "password file") {
			t.Errorf("%s: stderr %q", flag, stderr)
		}
		if len(opened) != 0 || h.stdin.String() != "unread" {
			t.Errorf("%s: a source was read: opened %v, stdin %q", flag, opened, h.stdin.String())
		}
		if len(h.restic.calls) != callsBefore+1 || h.restic.calls[callsBefore].sub != "version" {
			t.Errorf("%s: restic calls %v", flag, h.restic.subs()[callsBefore:])
		}
		h.assertHostUnchanged(before)
	}
}

// П17: the password file of the repository names the path when it does not open it.
func TestAWrongPasswordFileOfAChangeOfKeysNamesThePathAndNeverAsksTheTerminal(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	h.write(h.path("secrets/restic-extra.pass"), "another\n", 0o600)
	term := h.terminalIs()
	before := h.hostTree()
	h.stdinIs(s3Marker + "-2")
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-2", "--secret-key-stdin", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	if !strings.Contains(stderr, h.path("secrets/restic-extra.pass")) || len(term.prompts) != 0 {
		t.Fatalf("stderr %q, prompts %q", stderr, term.prompts)
	}
	h.assertHostUnchanged(before)
	if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
		t.Fatalf("temporary files %v", left)
	}
}

// П17
func TestAChangeOfKeysWithoutThePasswordFileIsPasswordFileMissing(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	ok(t, os.Remove(h.path("secrets/restic-extra.pass")))
	before := h.hostTree()
	callsBefore := len(h.restic.calls)
	h.stdinIs(s3Marker + "-2")
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-2", "--secret-key-stdin", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PASSWORD_FILE_MISSING")
	if !strings.Contains(stderr, h.path("secrets/restic-extra.pass")) {
		t.Fatalf("stderr %q", stderr)
	}
	if len(h.restic.calls) != callsBefore+1 {
		t.Fatalf("restic calls %v", h.restic.subs()[callsBefore:])
	}
	h.assertHostUnchanged(before)
}

// П17: a leftover password file that A1 rejects is SECRET_FILE_REJECTED
// and stays as it is.
func TestALeftoverPasswordFileThatA1RejectsIsRefusedAndKept(t *testing.T) {
	pass := func(h *setupHost) string { return h.path("secrets/restic-extra.pass") }
	for name, spoil := range map[string]func(h *setupHost){
		"owned by another uid": func(h *setupHost) {
			h.deps.readOwned = func(p string, _ uint32) ([]byte, error) {
				if strings.HasSuffix(p, ".pass") {
					return secrets.ReadOwned(p, uint32(os.Getuid())+1000)
				}
				return secrets.ReadOwned(p, uint32(os.Getuid()))
			}
		},
		"mode 0644":            func(h *setupHost) { ok(t, os.Chmod(pass(h), 0o644)) },
		"a symbolic link": func(h *setupHost) {
			target := h.path("outside/real.pass")
			h.write(target, "Q\n", 0o600)
			ok(t, os.Remove(pass(h)))
			ok(t, os.Symlink(target, pass(h)))
		},
		"a directory": func(h *setupHost) {
			ok(t, os.Remove(pass(h)))
			ok(t, os.Mkdir(pass(h), 0o700))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newSetupHost(t)
			h.deps.readOwned = ownedAsUID(uint32(os.Getuid()))
			h.s3Repo().answers("init", "Fatal: create repository failed: Internal Error", 1)
			h.s3Cmd()
			spoil(h)
			h.s3Repo().script = nil
			before := h.hostTree()
			callsBefore := len(h.restic.calls)
			code, _, stderr := h.s3Cmd()
			assertRefusal(t, code, stderr, exitUsage, "SECRET_FILE_REJECTED")
			if !strings.Contains(stderr, pass(h)) {
				t.Errorf("stderr %q", stderr)
			}
			if len(h.restic.calls) != callsBefore+1 {
				t.Errorf("restic calls %v", h.restic.subs()[callsBefore:])
			}
			h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
			if d := diff(before, h.hostTree()); len(d) != 0 {
				t.Errorf("the host changed: %v", d)
			}
		})
	}
}

// ownedAsUID is the owner-checked read of A1 for files of uid.
func ownedAsUID(uid uint32) func(string, uint32) ([]byte, error) {
	return func(p string, _ uint32) ([]byte, error) { return secrets.ReadOwned(filepath.Clean(p), uid) }
}
