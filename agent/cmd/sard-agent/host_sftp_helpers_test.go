// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The conventions of A8b-2 (docs/specs/agent/host-setup.feature): A_SFTP,
// the server nas.example.com with an ed25519 key of fingerprint FP-ED and an
// ecdsa key of fingerprint FP-EC, the home H of the service user in passwd.
const (
	sftpAddress = "sftp:backup@nas.example.com:/srv/extra"
	sftpHost    = "nas.example.com"
)

type serverKey struct{ keyType, blob string }

func (k serverKey) fingerprint() string {
	raw, _ := base64.StdEncoding.DecodeString(k.blob)
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func (k serverKey) line(host string) string { return host + " " + k.keyType + " " + k.blob + "\n" }

func newServerKey(keyType string, payload byte) serverKey {
	var b []byte
	for _, part := range []string{keyType, strings.Repeat(string([]byte{payload}), 32)} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(part)))
		b = append(b, part...)
	}
	return serverKey{keyType, base64.StdEncoding.EncodeToString(b)}
}

var (
	keyED    = newServerKey("ssh-ed25519", 'e')
	keyEC    = newServerKey("ecdsa-sha2-nistp256", 'c')
	keyRSA   = newServerKey("ssh-rsa", 'r')
	keyOldED = newServerKey("ssh-ed25519", 'o')
)

// sshCall is one run of a program of the OpenSSH client.
type sshCall struct {
	program string
	args    []string
	env     []string
	runAs   *restic.RunAs
}

// sshServer is the server nas.example.com and the programs of the client
// talking to it: what ssh-keyscan sees, what the login says.
type sshServer struct {
	keys []serverKey
	// authorized: the login is accepted; otherwise it says Permission denied.
	authorized bool
	// loginStderr and loginCode, if set, are the answer of the login check.
	loginStderr string
	loginCode   int
	// keygenStderr, if set, makes ssh-keygen fail with it.
	keygenStderr string
	// onScan runs when ssh-keyscan is asked, before it answers.
	onScan func()
	// hang makes the programs named never end until they are stopped.
	hang map[string]bool
	// terminated lists the programs that were stopped.
	terminated []string
	hostname   string
}

// fakeSSH is the OpenSSH client on the host: it records what it is asked
// and answers from the server; ssh-keygen really writes the key files.
type fakeSSH struct {
	t      *testing.T
	fsys   *fakeFS
	srv    *sshServer
	mu     sync.Mutex
	calls  []sshCall
	hungCh chan string
}

func (f *fakeSSH) Run(ctx context.Context, cmd restic.Command) (int, error) {
	program := filepath.Base(cmd.Path)
	f.mu.Lock()
	f.calls = append(f.calls, sshCall{program: program, args: append([]string(nil), cmd.Args...), env: append([]string(nil), cmd.Env...), runAs: cmd.RunAs})
	f.mu.Unlock()
	if f.srv.hang[program] {
		f.hungCh <- program
		<-ctx.Done()
		f.mu.Lock()
		f.srv.terminated = append(f.srv.terminated, program)
		f.mu.Unlock()
		return -1, nil
	}
	switch program {
	case "ssh-keyscan":
		return f.keyscan(cmd)
	case "ssh-keygen":
		return f.keygen(cmd)
	case "sftp":
		return f.login(cmd)
	}
	f.t.Fatalf("unexpected program %s", cmd.Path)
	return 0, nil
}

func (f *fakeSSH) keyscan(cmd restic.Command) (int, error) {
	if f.srv.onScan != nil {
		f.srv.onScan()
	}
	host := cmd.Args[len(cmd.Args)-1]
	name := host
	if i := slices.Index(cmd.Args, "-p"); i >= 0 && cmd.Args[i+1] != "22" {
		name = "[" + host + "]:" + cmd.Args[i+1]
	}
	for _, k := range f.srv.keys {
		cmd.Stdout([]byte(strings.TrimSuffix(k.line(name), "\n")))
	}
	if len(f.srv.keys) == 0 {
		cmd.Stderr([]byte("getaddrinfo " + host + ": Name or service not known"))
	}
	return 0, nil
}

func (f *fakeSSH) login(cmd restic.Command) (int, error) {
	switch {
	case f.srv.loginStderr != "":
		cmd.Stderr([]byte(f.srv.loginStderr))
		return f.srv.loginCode, nil
	case !f.srv.authorized:
		cmd.Stderr([]byte("backup@" + sftpHost + ": Permission denied (publickey)."))
		return 255, nil
	}
	return 0, nil
}

func (f *fakeSSH) keygen(cmd restic.Command) (int, error) {
	if f.srv.keygenStderr != "" {
		cmd.Stderr([]byte(f.srv.keygenStderr))
		return 1, nil
	}
	path := cmd.Args[slices.Index(cmd.Args, "-f")+1]
	if slices.Contains(cmd.Args, "-y") {
		cmd.Stdout([]byte("ssh-ed25519 AAAAderivedkey sard-agent@" + f.srv.hostname))
		return 0, nil
	}
	if err := os.WriteFile(path, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nPRIVATE-KEY-MARKER\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path+".pub", []byte("ssh-ed25519 AAAAnewkey sard-agent@"+f.srv.hostname+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.fsys.setOwner(path, int(f.fsys.defaultUID), int(f.fsys.defaultUID))
	f.fsys.setOwner(path+".pub", int(f.fsys.defaultUID), int(f.fsys.defaultUID))
	return 0, nil
}

// called are the runs of a program.
func (f *fakeSSH) called(program string) []sshCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var got []sshCall
	for _, c := range f.calls {
		if c.program == program {
			got = append(got, c)
		}
	}
	return got
}

func (f *fakeSSH) none() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls) == 0
}

// sftpHostWorld is the world of the SFTP scenarios: the setupHost, a home
// H for the service user, the client programs in the PATH of restic, and a
// server.
type sftpHostWorld struct {
	*setupHost
	srv *sshServer
	ssh *fakeSSH
}

func (h *sftpHostWorld) homeDir() string { return h.path("home") }
func (h *sftpHostWorld) sshDir() string  { return filepath.Join(h.homeDir(), ".ssh") }
func (h *sftpHostWorld) sshFile(name string) string {
	return filepath.Join(h.sshDir(), name)
}

// programsDir is the PATH of restic with the four programs of the client.
func (h *sftpHostWorld) programsDir() string { return h.path("bin") }

func newSFTPHost(t *testing.T) *sftpHostWorld {
	t.Helper()
	h := &sftpHostWorld{setupHost: newSetupHost(t)}
	home := h.homeDir()
	h.write(filepath.Join(home, ".keep"), "", 0o600)
	ok(t, os.Remove(filepath.Join(home, ".keep")))
	ok(t, os.Chmod(home, 0o755))
	h.fsys.setOwner(home, serviceUID, serviceUID)
	service := svcUser
	service.Home = home
	h.people[0] = service
	for _, program := range []string{"ssh", "sftp", "ssh-keygen", "ssh-keyscan"} {
		h.write(filepath.Join(h.programsDir(), program), "#!/bin/sh\n", 0o755)
	}
	h.deps.pathEnv = h.programsDir()
	h.srv = &sshServer{keys: []serverKey{keyED, keyEC}, authorized: true, hang: map[string]bool{}, hostname: "host1"}
	h.ssh = &fakeSSH{t: t, fsys: h.fsys, srv: h.srv, hungCh: make(chan string, 4)}
	h.deps.sshExec = h.ssh
	h.deps.hostname = func() (string, error) { return h.srv.hostname, nil }
	return h
}

// sftpCmd is "repo add extra A_SFTP --host-key-fingerprint FP-ED" plus extra flags.
func (h *sftpHostWorld) sftpCmd(extra ...string) (int, string, string) {
	return h.sftpAt(sftpAddress, append([]string{"--host-key-fingerprint", keyED.fingerprint()}, extra...)...)
}

// sftpAt is repo add extra <address> with the flags given, as root.
func (h *sftpHostWorld) sftpAt(address string, flags ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "add", "extra", address, "--config", "C"}, flags...)...)
}

// knownHosts puts the lines into H/.ssh/known_hosts (the key of the host is "known").
func (h *sftpHostWorld) putSSH(name, content string, mode os.FileMode) {
	h.t.Helper()
	ok(h.t, os.MkdirAll(h.sshDir(), 0o700))
	h.write(h.sshFile(name), content, mode)
}

// keyAndHostKnown is "ключ хоста известен и ключ SSH есть".
func (h *sftpHostWorld) keyAndHostKnown() {
	h.t.Helper()
	h.putSSH("known_hosts", keyED.line(sftpHost), 0o600)
	h.putSSH("id_ed25519", "PRIVATE-KEY-MARKER", 0o600)
	h.putSSH("id_ed25519.pub", "ssh-ed25519 AAAAexistingkey sard-agent@host1\n", 0o644)
}

// sshTree is H/.ssh as the tests compare it: every file with its mode,
// owner and content.
func (h *sftpHostWorld) sshTree() map[string]string {
	h.t.Helper()
	got, err := tree(h.homeDir(), h.fsys)
	ok(h.t, err)
	return got
}

// assertSSHUnchanged is "файлы ssh не изменены".
func (h *sftpHostWorld) assertSSHUnchanged(before map[string]string) {
	h.t.Helper()
	if d := diff(before, h.sshTree()); len(d) > 0 {
		h.t.Fatalf("the ssh files changed: %v", d)
	}
}

// assertNoSSHProgram is "ни ssh, ни sftp, ни ssh-keyscan, ни ssh-keygen не запускались".
func (h *sftpHostWorld) assertNoSSHProgram() {
	h.t.Helper()
	if !h.ssh.none() {
		h.t.Fatalf("programs of the client ran: %+v", h.ssh.calls)
	}
}

// assertNothingChanged is "хост не изменён" for a refusal under the locks: the
// ssh files are as they were, and no password file, fragment, temporary
// file, restart or audit line is left.
func (h *sftpHostWorld) assertNothingChanged(sshBefore map[string]string) {
	h.t.Helper()
	h.assertSSHUnchanged(sshBefore)
	h.assertNoSFTPTraces()
	if len(h.log.lines) != 0 {
		h.t.Fatalf("audit lines: %q", h.log.lines)
	}
}

func (h *sftpHostWorld) assertNoSFTPTraces() {
	h.t.Helper()
	h.assertAbsent(h.path("secrets/restic-extra.pass"), h.path("agent.d/repo-extra.yaml"))
	if left := append(h.tempFilesIn(h.secretsDir()), h.tempFilesIn(h.sshDir())...); len(left) != 0 {
		h.t.Fatalf("temporary files: %v", left)
	}
	if h.sd.touched() {
		h.t.Fatalf("systemctl was called: %v", h.sd.calls)
	}
}

func (h *sftpHostWorld) sftpRepo() *fakeRepo { return h.restic.repo(sftpAddress) }

// assertSFTPValuesHidden is "значения не раскрыты" for the SFTP scenarios:
// the private key never shows either.
func (h *sftpHostWorld) assertSFTPValuesHidden(outputs ...string) {
	h.t.Helper()
	h.assertValuesHidden(outputs...)
	for _, out := range append(outputs, h.log.lines...) {
		if strings.Contains(out, "PRIVATE-KEY-MARKER") {
			h.t.Fatalf("the private key is exposed:\n%s", out)
		}
	}
}

// connectedSFTP is a successful first command.
func (h *sftpHostWorld) connectedSFTP() string {
	h.t.Helper()
	code, stdout, stderr := h.sftpCmd()
	if code != exitOK {
		h.t.Fatalf("code %d: %s", code, stderr)
	}
	h.sd.calls, h.log.lines = nil, nil
	return stdout
}

var _ = hostsetup.User{}
