// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// Rule "enroll под sudo пишет идентичность для пользователя службы".

// rootEnrollDeps is the command run by root, the service user being
// sard-agent (uid 990) and backup (uid 991); chown is the fake's.
func rootEnrollDeps(fsys *fakeFS, people ...hostsetup.User) enrollDeps {
	if people == nil {
		people = []hostsetup.User{svcUser, backupUser}
	}
	d := testEnrollDeps(fixedHostname)
	d.euid = 0
	d.lookupUser = users(people...)
	d.fs = fsys
	d.chownFile = fsys.chownFile
	return d
}

func (f *fakeFixture) enrollWith(deps enrollDeps, extra ...string) (int, string, string) {
	var out, errOut strings.Builder
	args := append([]string{"--config", f.h.configPath, "--token", f.token}, extra...)
	code := runEnrollWithDeps(context.Background(), args, &out, &errOut, deps)
	return code, out.String(), errOut.String()
}

// useDirs points tls.* at the given directories and rewrites the config.
func (f *fakeFixture) useDirs(keyDir, certDir, caDir string) {
	f.h.keyDir, f.h.certDir, f.h.caDir = keyDir, certDir, caDir
	f.h.keyFile, f.h.certFile, f.h.caFile = filepath.Join(keyDir, "agent.key"), filepath.Join(certDir, "agent.pem"), filepath.Join(caDir, "ca.pem")
	f.h.configPath = f.h.writeConfig(f.h.testing, f.h.address)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s: mode %v, want %v", path, info.Mode().Perm(), want)
	}
}

func TestEnrollmentUnderSudoHandsTheIdentityToTheServiceUser(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	fsys := newFakeFS()
	code, _, errOut := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitOK)
	if errOut != "" {
		t.Fatalf("stderr %q", errOut)
	}
	if len(fsys.events) != 3 {
		t.Fatalf("events %v", fsys.events)
	}
	for _, e := range fsys.events {
		if !strings.HasSuffix(e, " 990:990") || !strings.Contains(e, ".sard-enroll-") {
			t.Errorf("event %q: want the temporary file of the identity given to 990:990", e)
		}
	}
	assertMode(t, f.h.keyFile, 0o600)
	assertMode(t, f.h.certFile, 0o644)
	assertMode(t, f.h.caFile, 0o644)
	for _, dir := range []string{f.h.keyDir, f.h.certDir, f.h.caDir} {
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s holds %v: want the file alone, no temporary and no lock file", dir, entries)
		}
	}
}

func TestIdentityFileModesDoNotDependOnTheUmaskUnderSudo(t *testing.T) {
	for _, mask := range []int{0o000, 0o077} {
		old := syscall.Umask(mask)
		f := newSucceedingFakeFixture(t, "a1")
		code, _, _ := f.enrollWith(rootEnrollDeps(newFakeFS()))
		syscall.Umask(old)
		assertCode(t, code, exitOK)
		assertMode(t, f.h.keyFile, 0o600)
		assertMode(t, f.h.certFile, 0o644)
		assertMode(t, f.h.caFile, 0o644)
	}
}

func TestForceUnderSudoReplacesAnIdentityOfRootWithTheServiceUsersFiles(t *testing.T) {
	f := newSucceedingFakeFixture(t, "old-agent")
	code, _, _ := f.enrollWith(testEnrollDeps(fixedHostname))
	assertCode(t, code, exitOK)
	fsys := newFakeFS()
	f.srv.answer = succeedingAnswer(f.ca, "new-agent")
	code, out, _ := f.enrollWith(rootEnrollDeps(fsys), "--force")
	assertCode(t, code, exitOK)
	if len(fsys.events) != 3 || !strings.Contains(out, "new-agent") {
		t.Fatalf("events %v, stdout %q", fsys.events, out)
	}
}

func TestAFailedOwnerChangeAfterTheRegistrationLeavesNoFiles(t *testing.T) {
	for i, file := range []string{"tls.key_file", "tls.cert_file", "tls.ca_file"} {
		t.Run(file, func(t *testing.T) {
			f := newSucceedingFakeFixture(t, "agent-x")
			fsys := newFakeFS()
			fsys.failChownAt = i + 1
			code, _, errOut := f.enrollWith(rootEnrollDeps(fsys))
			assertCode(t, code, exitWrite)
			for _, want := range []string{"agent-x", "token has been spent", "new one is required"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr lacks %q: %s", want, errOut)
				}
			}
			for _, dir := range []string{f.h.keyDir, f.h.certDir, f.h.caDir} {
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Errorf("%s holds %v", dir, entries)
				}
			}
		})
	}
}

func TestAFailedOwnerChangeWithForceKeepsThePreviousFiles(t *testing.T) {
	f := newSucceedingFakeFixture(t, "agent-x")
	if code, _, _ := f.enrollWith(testEnrollDeps(fixedHostname)); code != exitOK {
		t.Fatal("first enrollment failed")
	}
	before := map[string][]byte{}
	for _, p := range []string{f.h.keyFile, f.h.certFile, f.h.caFile} {
		before[p], _ = os.ReadFile(p)
	}
	fsys := newFakeFS()
	fsys.failChownAt = 1
	code, _, _ := f.enrollWith(rootEnrollDeps(fsys), "--force")
	assertCode(t, code, exitWrite)
	for p, content := range before {
		if got, _ := os.ReadFile(p); string(got) != string(content) {
			t.Errorf("%s changed", p)
		}
	}
}

func TestEnrollmentFromTheServiceUserChangesNoOwner(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	deps := testEnrollDeps(fixedHostname)
	fsys := newFakeFS()
	deps.fs = fsys
	code, _, _ := f.enrollWith(deps)
	assertCode(t, code, exitOK)
	if fsys.chowns() != 0 {
		t.Fatalf("events %v", fsys.events)
	}
}

func TestEnrollmentFromAnotherUserIsRefusedBeforeTheServerAndTheToken(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	deps := testEnrollDeps(fixedHostname)
	deps.euid = aliceUID
	code, out, errOut := f.enrollWith(deps)
	assertRefusal(t, code, errOut, exitUsage, "PRIVILEGES_REQUIRED")
	if !strings.Contains(errOut, "sudo sard-agent enroll") || strings.Contains(errOut, "sudo -u") {
		t.Fatalf("stderr %q", errOut)
	}
	if f.srv.callCount() != 0 || strings.Contains(out+errOut, f.token) || strings.Contains(out+errOut, "sard_") {
		t.Fatalf("server calls %d, output %q %q", f.srv.callCount(), out, errOut)
	}
	for _, dir := range []string{f.h.keyDir, f.h.certDir, f.h.caDir} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s holds %v", dir, entries)
		}
	}
}

func TestTheRightToEnrollIsCheckedBeforeTheTokenSource(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	deps := testEnrollDeps(fixedHostname)
	deps.euid = aliceUID
	var out, errOut strings.Builder
	code := runEnrollWithDeps(context.Background(), []string{"--config", f.h.configPath}, &out, &errOut, deps)
	assertRefusal(t, code, errOut.String(), exitUsage, "PRIVILEGES_REQUIRED")
	if strings.Contains(errOut.String(), "--token-file") {
		t.Fatalf("stderr lists the token sources: %q", errOut.String())
	}
}

func TestEnrollmentUnderSudoWithoutTheServiceUserIsRefusedBeforeTheServer(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	deps := rootEnrollDeps(newFakeFS())
	deps.lookupUser = users()
	code, _, errOut := f.enrollWith(deps)
	assertRefusal(t, code, errOut, exitUsage, "SERVICE_USER_UNKNOWN")
	if !strings.Contains(errOut, "sard-agent") || f.srv.callCount() != 0 {
		t.Fatalf("stderr %q, server calls %d", errOut, f.srv.callCount())
	}
}

func TestEnrollmentUnderSudoTakesTheServiceUserFromTheConfig(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	data, _ := os.ReadFile(f.h.configPath)
	ok(t, os.WriteFile(f.h.configPath, append(data, []byte("service:\n  user: backup\n")...), 0o600))
	fsys := newFakeFS()
	code, _, _ := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitOK)
	for _, e := range fsys.events {
		if !strings.HasSuffix(e, " 991:991") {
			t.Errorf("event %q", e)
		}
	}
}

// Rule "Каталог tls.* под sudo" (Р25а).

func TestAMissingTLSDirectoryIsCreatedForTheServiceUserUnderSudo(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	f := newSucceedingFakeFixture(t, "a1")
	dir := filepath.Join(f.h.dir, "tls")
	f.useDirs(dir, dir, dir)
	fsys := newFakeFS()
	code, _, _ := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitOK)
	assertMode(t, dir, 0o700)
	if o, _ := fsys.ownerOf(dir); o != (ownerRec{990, 990}) {
		t.Fatalf("directory owner %+v", o)
	}
	assertMode(t, f.h.keyFile, 0o600)
}

func TestEveryMissingDirectoryOfTheTLSFilesIsCreated(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	keyDir, pubDir := filepath.Join(f.h.dir, "tls-key"), filepath.Join(f.h.dir, "tls-pub")
	f.useDirs(keyDir, pubDir, pubDir)
	code, _, _ := f.enrollWith(rootEnrollDeps(newFakeFS()))
	assertCode(t, code, exitOK)
	assertMode(t, keyDir, 0o700)
	assertMode(t, pubDir, 0o700)
}

func TestAMissingParentOfTheTLSDirectoryIsAWriteErrorAndNothingIsCreated(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	dir := filepath.Join(f.h.dir, "etc", "sard", "tls")
	f.useDirs(dir, dir, dir)
	code, _, errOut := f.enrollWith(rootEnrollDeps(newFakeFS()))
	assertCode(t, code, exitWrite)
	for _, want := range []string{"tls.key_file", filepath.Join(f.h.dir, "etc")} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q: %s", want, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(f.h.dir, "etc")); err == nil {
		t.Fatal("a directory was created")
	}
	if f.srv.callCount() != 0 || strings.Contains(errOut, f.token) {
		t.Fatalf("server calls %d", f.srv.callCount())
	}
}

func TestAnExistingTLSDirectoryIsLeftAlone(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	dir := filepath.Join(f.h.dir, "tls")
	ok(t, os.Mkdir(dir, 0o755))
	f.useDirs(dir, dir, dir)
	fsys := newFakeFS()
	code, _, _ := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitOK)
	assertMode(t, dir, 0o755)
	if _, ok := fsys.ownerOf(dir); ok {
		t.Fatal("the owner of an existing directory was changed")
	}
}

func TestADirectoryMadeByAFailedEnrollmentIsRemoved(t *testing.T) {
	cases := map[string]func(f *fakeFixture) enrollDeps{
		"TOKEN_USED": func(f *fakeFixture) enrollDeps {
			f.srv.answer = failingAnswer(codes.Unauthenticated, "TOKEN_USED")
			return rootEnrollDeps(newFakeFS())
		},
		"the server does not accept the connection": func(f *fakeFixture) enrollDeps {
			f.h.address = "127.0.0.1:1"
			f.h.configPath = f.h.writeConfig(f.h.testing, f.h.address)
			return rootEnrollDeps(newFakeFS())
		},
		"the CA fingerprint differs from the token": func(f *fakeFixture) enrollDeps {
			f.token = newToken(f.h.testing, strings.Repeat("0", 64))
			return rootEnrollDeps(newFakeFS())
		},
		"writing a file fails": func(f *fakeFixture) enrollDeps {
			fsys := newFakeFS()
			fsys.failChownAt = 3 // the directory is 1, the key 2, the certificate 3
			return rootEnrollDeps(fsys)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSucceedingFakeFixture(t, "a1")
			dir := filepath.Join(f.h.dir, "tls")
			f.useDirs(dir, dir, dir)
			deps := setup(f)
			f.useDirs(dir, dir, dir)
			code, _, _ := f.enrollWith(deps)
			if code == exitOK {
				t.Fatal("the enrollment succeeded")
			}
			if _, err := os.Stat(dir); err == nil {
				t.Fatalf("the directory %s was left", dir)
			}
		})
	}
}

func TestADirectoryMadeByAnInterruptedEnrollmentIsRemoved(t *testing.T) {
	h, token, srv := newBlockingFixture(t)
	dir := filepath.Join(h.dir, "tls")
	h.keyDir, h.certDir, h.caDir = dir, dir, dir
	h.keyFile, h.certFile, h.caFile = filepath.Join(dir, "agent.key"), filepath.Join(dir, "agent.pem"), filepath.Join(dir, "ca.pem")
	h.configPath = h.writeConfig(h.testing, h.address)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var out, errOut strings.Builder
		done <- runEnrollWithDeps(ctx, []string{"--config", h.configPath, "--token", token}, &out, &errOut, rootEnrollDeps(newFakeFS()))
	}()
	<-srv.started
	cancel()
	if code := <-done; code != exitTemporary {
		t.Fatalf("code = %d", code)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the directory was left")
	}
}

func TestAMissingTLSDirectoryIsNotCreatedForTheServiceUser(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	dir := filepath.Join(f.h.dir, "tls")
	f.useDirs(dir, dir, dir)
	code, _, errOut := f.enrollWith(testEnrollDeps(fixedHostname))
	assertCode(t, code, exitWrite)
	if !strings.Contains(errOut, dir) || f.srv.callCount() != 0 {
		t.Fatalf("stderr %q, server calls %d", errOut, f.srv.callCount())
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the directory was created")
	}
}

func TestAnEnrollmentDirectoryThatCannotBeCreatedIsAWriteError(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	dir := filepath.Join(f.h.dir, "tls")
	f.useDirs(dir, dir, dir)
	fsys := newFakeFS()
	fsys.failOn, fsys.failPath = "mkdir", "tls"
	code, _, errOut := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitWrite)
	if !strings.Contains(errOut, dir) || f.srv.callCount() != 0 {
		t.Fatalf("stderr %q, server calls %d", errOut, f.srv.callCount())
	}
}

func TestADirectoryMadeBeforeAFailedLaterOneIsTakenBack(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	keyDir, pubDir := filepath.Join(f.h.dir, "tls-key"), filepath.Join(f.h.dir, "tls-pub")
	f.useDirs(keyDir, pubDir, pubDir)
	fsys := newFakeFS()
	fsys.failOn, fsys.failPath = "mkdir", "tls-pub"
	code, _, _ := f.enrollWith(rootEnrollDeps(fsys))
	assertCode(t, code, exitWrite)
	if _, err := os.Stat(keyDir); err == nil {
		t.Fatal("the first directory was left")
	}
}

func TestEnrollHelpTellsToRunItWithSudo(t *testing.T) {
	code, out, _ := runEnrollCmdTest("--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"sudo sard-agent enroll", "service user", "PRIVILEGES_REQUIRED"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sudo -u") {
		t.Fatal("help mentions sudo -u")
	}
}
