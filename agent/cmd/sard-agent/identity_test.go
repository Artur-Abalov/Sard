// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// docs/specs/agent/agent-tls-identity.feature (OQ-027).
const incompleteEnrollment = "tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`"

// identityPEM returns a new key (PKCS #8) and a certificate issued on it.
func identityPEM(t *testing.T) (key, cert []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
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

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	accepted = &atomic.Int32{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	return l.Addr().String(), accepted
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

// A1 reports the key's permissions before the pair is compared.
func TestKeyPermissionsAreReportedBeforeTheMismatch(t *testing.T) {
	dir := t.TempDir()
	writeIdentity(t, dir)
	other, _ := identityPEM(t)
	writeFile(t, filepath.Join(dir, "agent.key"), other, 0o644)
	code, _, errOut := runAgent("--config", identityConfig(t, dir, "127.0.0.1:1"))
	if code != 1 || !strings.Contains(errOut, "tls.key_file") || !strings.Contains(errOut, "-rw-r--r--") || strings.Contains(errOut, incompleteEnrollment) {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestAFileProblemNamesTheKeyAndPathNotTheContent(t *testing.T) {
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
