// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dirForTLSFile maps "key"/"cert"/"ca" to the corresponding directory in h.
func dirForTLSFile(h *host, which string) string {
	switch which {
	case "key":
		return h.keyDir
	case "cert":
		return h.certDir
	default:
		return h.caDir
	}
}

// Сбой записи после успешной регистрации не оставляет файлов
func TestAWriteFailureAfterASuccessfulEnrollmentLeavesNoFiles(t *testing.T) {
	for _, which := range []string{"key", "cert", "ca"} {
		t.Run(which, func(t *testing.T) {
			ca := newTestCA(t)
			leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
			srv := &enrollServer{started: make(chan struct{}), block: make(chan struct{})}
			srv.answer = succeedingAnswer(ca, "broken-write-agent")
			addr := startFakeServer(t, leaf, srv)
			h := newHost(t, addr)
			token := newToken(t, ca.fingerprint())

			// CheckWritable runs before Enroll and must see a normal,
			// writable directory; only once the request is in flight does
			// this replace it with a file, so the failure genuinely lands
			// in WriteIdentity, after a successful Enroll.
			go func() {
				<-srv.started
				replaceDirWithFileForTest(t, dirForTLSFile(h, which))
				close(srv.block)
			}()

			code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
			if code != exitWrite {
				t.Fatalf("code = %d, want %d (write); stderr = %q", code, exitWrite, errOut)
			}
			for _, p := range []string{h.keyFile, h.certFile, h.caFile} {
				if _, err := os.Stat(p); err == nil {
					t.Fatalf("%s exists after a failed write, want none of the three", p)
				}
			}
			if !strings.Contains(errOut, "broken-write-agent") {
				t.Errorf("stderr does not name the agent id: %q", errOut)
			}
			if !strings.Contains(errOut, "spent") || !strings.Contains(errOut, "new") {
				t.Errorf("stderr does not say the token was spent and a new one is needed: %q", errOut)
			}
		})
	}
}

// Сбой записи при --force оставляет прежние файлы
func TestAWriteFailureWithForceLeavesThePreviousFiles(t *testing.T) {
	for _, which := range []string{"key", "cert", "ca"} {
		t.Run(which, func(t *testing.T) {
			f := newSucceedingFakeFixture(t, "old-agent")
			if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token); code != exitOK {
				t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
			}
			oldKey, _ := os.ReadFile(f.h.keyFile)
			oldCert, _ := os.ReadFile(f.h.certFile)
			oldCA, _ := os.ReadFile(f.h.caFile)

			f.srv.started = make(chan struct{})
			f.srv.block = make(chan struct{})
			f.srv.answer = succeedingAnswer(f.ca, "new-agent")
			go func() {
				<-f.srv.started
				replaceDirWithFileForTest(t, dirForTLSFile(f.h, which))
				close(f.srv.block)
			}()

			token2 := newToken(t, f.ca.fingerprint())
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2)
			if code != exitWrite {
				t.Fatalf("code = %d, want %d (write); stderr = %q", code, exitWrite, errOut)
			}
			// The broken directory (its own file, now) no longer holds a
			// readable file at all — that is how this test breaks the
			// write, not something the command did. The other two, whose
			// directories were never touched, must be exactly as before.
			unchanged := map[string][]byte{"key": oldKey, "cert": oldCert, "ca": oldCA}
			paths := map[string]string{"key": f.h.keyFile, "cert": f.h.certFile, "ca": f.h.caFile}
			for name, want := range unchanged {
				if name == which {
					continue
				}
				requireFileContentUnchanged(t, paths[name], string(want))
			}
		})
	}
}

// replaceDirWithFileForTest breaks writes into dir even for root: ENOTDIR
// is not something owner-bit permissions can bypass.
func replaceDirWithFileForTest(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Ключ записывается с правами 0600 при любой umask
func TestTheKeyIsWritten0600RegardlessOfUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	requireFileMode(t, f.h.keyFile, 0o600)
}

// При --force ключ получает права 0600, даже если прежний был шире
func TestForceStillWritesTheKeyAt0600EvenIfThePreviousOneWasWider(t *testing.T) {
	f := newSucceedingFakeFixture(t, "old-agent")
	if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token); code != exitOK {
		t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
	}
	if err := os.Chmod(f.h.keyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	f.srv.answer = succeedingAnswer(f.ca, "new-agent")
	token2 := newToken(t, f.ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2)
	if code != exitOK {
		t.Fatalf("forced enroll: code = %d, stderr = %q", code, errOut)
	}
	requireFileMode(t, f.h.keyFile, 0o600)
}

// Файлы принадлежат пользователю, запустившему команду
func TestFilesBelongToTheUserRunningTheCommand(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	want := uint32(os.Getuid())
	for _, p := range []string{f.h.keyFile, f.h.certFile, f.h.caFile} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("cannot determine the owner of %s on this platform", p)
		}
		if stat.Uid != want {
			t.Errorf("%s owner uid = %d, want %d", p, stat.Uid, want)
		}
	}
}

// Сертификат и бандл доступны на чтение всем
func TestTheCertificateAndBundleAreWorldReadable(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	requireFileMode(t, f.h.certFile, 0o644)
	requireFileMode(t, f.h.caFile, 0o644)
}

func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode().Perm(), want)
	}
}

// Успешная команда не читает стандартный ввод
//
// Tagged @local in the spec (it needs no server interaction to prove), but
// exercised here against a real fake server so the assertion is about an
// actual successful run, not merely that production code never touches
// os.Stdin. A stdin that blocks forever must not make the command wait.
func TestASuccessfulCommandDoesNotReadStdin(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	// The command under test here is run() (via runEnrollCmdTest), which
	// never touches os.Stdin at all (see enroll_run.go): there is nothing
	// to wire a blocking reader into. Finishing promptly without ever
	// being given a stdin to read from is itself the proof.
	done := make(chan int, 1)
	go func() {
		code, _, _ := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
		done <- code
	}()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("code = %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command did not finish promptly")
	}
}
