// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// docs/specs/agent/agent-tls-identity.feature (OQ-027).
const incompleteEnrollment = "tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`"

// identityPEM returns a new key (PKCS #8) and a certificate issued on it:
// the identity of agent 2222… of tenant 1111… with the role source.
func identityPEM(t *testing.T) (key, cert []byte) {
	t.Helper()
	return rolePair(t, testIdentityURI, "sard://roles/source")
}

// writeIdentity writes ca.pem, agent.pem and agent.key (one pair) into dir
// and returns the CA path.
func writeIdentity(t *testing.T, dir string) string {
	t.Helper()
	key, cert := identityPEM(t)
	ca := filepath.Join(dir, "ca.pem")
	writeFile(t, ca, selfSignedPEM(t), 0o600)
	writeFile(t, filepath.Join(dir, "agent.pem"), cert, 0o644)
	writeFile(t, filepath.Join(dir, "agent.key"), key, 0o600)
	return ca
}

// writeExecutable writes a file that a test later runs, holding
// syscall.ForkLock while the file is open for writing. os/exec forks under
// ForkLock.RLock, so no parallel test can fork (and leak this writable fd
// into a child that has not yet exec'd) before the fd is closed; otherwise
// running the file fails with "text file busy" (ETXTBSY), golang/go#22315.
func writeExecutable(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	syscall.ForkLock.Lock()
	err := os.WriteFile(path, data, mode)
	syscall.ForkLock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if mode&0o111 != 0 {
		writeExecutable(t, path, data, mode)
		return
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// countingServer accepts TCP connections and counts them.
func countingServer(t *testing.T) (addr string, accepted *atomic.Int32) {
	t.Helper()
	addr, accepted, _ = signalingServer(t)
	return addr, accepted
}

// signalingServer is countingServer whose first channel is closed once the
// first connection has been accepted and counted.
func signalingServer(t *testing.T) (addr string, accepted *atomic.Int32, first <-chan struct{}) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	accepted = &atomic.Int32{}
	signal := make(chan struct{})
	var once sync.Once
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			once.Do(func() { close(signal) })
			_ = c.Close()
		}
	}()
	return l.Addr().String(), accepted, signal
}

// eventWait is the upper bound for waits that end on an event (the first
// accepted connection, the first registration): a passing run ends on the
// event, not on the limit. It is generous so that load and -race do not fail
// a test that merely started late. Tests that assert the absence of an event
// keep their own, short windows.
const eventWait = 10 * time.Second

// contextUntil is a context that ends after limit or as soon as event is
// closed (a nil event never fires). The caller defers the returned cancel.
func contextUntil(limit time.Duration, event <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	go func() {
		select {
		case <-event:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func identityConfig(t *testing.T, dir, addr string) string {
	t.Helper()
	return writeConfig(t, "server:\n  address: "+addr+"\n"+
		"tls: {ca_file: "+dir+"/ca.pem, cert_file: "+dir+"/agent.pem, key_file: "+dir+"/agent.key}\n"+
		"executor: {state_dir: "+dir+"/state}\n")
}

func requireNoKeyMaterial(t *testing.T, outputs ...string) {
	t.Helper()
	for _, out := range outputs {
		if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "CERTIFICATE") || strings.Contains(out, "MII") {
			t.Errorf("output reveals key material: %q", out)
		}
	}
}

func TestAKeyOfAnotherPairStopsTheAgentBeforeConnecting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIdentity(t, dir)
	other, _ := identityPEM(t)
	writeFile(t, filepath.Join(dir, "agent.key"), other, 0o600)
	addr, accepted := countingServer(t)
	code, out, errOut := runAgent("--config", identityConfig(t, dir, addr))
	if code != 1 || errOut != "sard-agent: "+incompleteEnrollment+"\n" {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	if strings.Contains(out, "connecting to") || accepted.Load() != 0 {
		t.Errorf("stdout = %q, connections = %d", out, accepted.Load())
	}
	requireNoKeyMaterial(t, out, errOut)
}

// П29: the pair is chosen before the modes are checked; without a consistent
// pair the pair error is reported, not the key's mode.
func TestTheMismatchIsReportedBeforeKeyPermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeIdentity(t, dir)
	other, _ := identityPEM(t)
	writeFile(t, filepath.Join(dir, "agent.key"), other, 0o644)
	code, _, errOut := runAgent("--config", identityConfig(t, dir, "127.0.0.1:1"))
	if code != 1 || errOut != "sard-agent: "+incompleteEnrollment+"\n" || strings.Contains(errOut, "-rw-r--r--") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestAFileProblemNamesTheKeyAndPathNotTheContent(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		file, key string
		data      []byte
	}{
		"missing certificate": {"agent.pem", "tls.cert_file", nil},
		"certificate not PEM": {"agent.pem", "tls.cert_file", []byte("MARKER-S")},
		"missing key":         {"agent.key", "tls.key_file", nil},
		"key not PEM":         {"agent.key", "tls.key_file", []byte("MARKER-S")},
	} {
		fileProblem(t, name, tc.file, tc.key, tc.data)
	}
}

// fileProblem replaces file (missing when data is nil) and starts the agent.
func fileProblem(t *testing.T, name, file, key string, data []byte) {
	t.Helper()
	dir := t.TempDir()
	writeIdentity(t, dir)
	path := filepath.Join(dir, file)
	if data == nil {
		_ = os.Remove(path)
	} else {
		writeFile(t, path, data, 0o600)
	}
	addr, accepted := countingServer(t)
	code, out, errOut := runAgent("--config", identityConfig(t, dir, addr))
	if code != 1 || !strings.Contains(errOut, key+` "`+path+`"`) || strings.Contains(errOut, incompleteEnrollment) {
		t.Errorf("%s: code = %d, stderr = %q", name, code, errOut)
	}
	if strings.Contains(out, "connecting to") || accepted.Load() != 0 || strings.Contains(errOut, "MARKER-S") {
		t.Errorf("%s: stdout = %q, stderr = %q, connections = %d", name, out, errOut, accepted.Load())
	}
}
