// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func writeAgentCert(t *testing.T, dir, agentID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse("sard://tenants/t1/agents/" + agentID)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: agentID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectIdentityFindsNoIdentityWhenNoFilesExist(t *testing.T) {
	dir := t.TempDir()
	files := config.TLS{CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	status, err := enroll.InspectIdentity(files)
	if err != nil {
		t.Fatalf("InspectIdentity: %v", err)
	}
	if status.Exists {
		t.Fatal("want no identity when no tls.* files exist")
	}
}

func TestInspectIdentityReadsTheAgentIDFromTheCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath := writeAgentCert(t, dir, "a1")
	files := config.TLS{CAFile: filepath.Join(dir, "ca.pem"), CertFile: certPath, KeyFile: filepath.Join(dir, "key.pem")}
	status, err := enroll.InspectIdentity(files)
	if err != nil {
		t.Fatalf("InspectIdentity: %v", err)
	}
	if !status.Exists {
		t.Fatal("want an existing identity")
	}
	if status.AgentID != "a1" {
		t.Fatalf("agent id = %q, want a1", status.AgentID)
	}
}

func TestInspectIdentityCountsAKeyAloneAsAnIdentityWithUnknownAgentID(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("not really a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := config.TLS{CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, "cert.pem"), KeyFile: keyPath}
	status, err := enroll.InspectIdentity(files)
	if err != nil {
		t.Fatalf("InspectIdentity: %v", err)
	}
	if !status.Exists {
		t.Fatal("want an existing identity from tls.key_file alone")
	}
	if status.AgentID != "" {
		t.Fatalf("agent id = %q, want unknown", status.AgentID)
	}
}

func TestInspectIdentityCountsAnUnparsableCertificateAsAnIdentity(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := config.TLS{CAFile: filepath.Join(dir, "ca.pem"), CertFile: certPath, KeyFile: filepath.Join(dir, "key.pem")}
	status, err := enroll.InspectIdentity(files)
	if err != nil {
		t.Fatalf("InspectIdentity: %v", err)
	}
	if !status.Exists {
		t.Fatal("want an existing identity from an unparsable certificate")
	}
	if status.AgentID != "" {
		t.Fatalf("agent id = %q, want unknown", status.AgentID)
	}
	if !status.Unreadable {
		t.Fatal("want Unreadable set so the CLI can say agent_id could not be read")
	}
}

func TestInspectIdentityIgnoresACABundleAlone(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := config.TLS{CAFile: caPath, CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	status, err := enroll.InspectIdentity(files)
	if err != nil {
		t.Fatalf("InspectIdentity: %v", err)
	}
	if status.Exists {
		t.Fatal("a lone tls.ca_file must not count as an identity (В19)")
	}
}
