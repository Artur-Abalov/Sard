// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// --- CA / certificate fixtures, mirroring agent/internal/enroll's own test
// helpers (trust_helpers_test.go), duplicated here because that package's
// test-only helpers are unexported and this is a different package.

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) fingerprint() string {
	sum := sha256.Sum256(ca.cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// leaf issues a server TLS certificate for names, signed by ca.
func (ca *testCA) leaf(t *testing.T, names []string, validFor time.Duration) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if validFor == 0 {
		validFor = 24 * time.Hour
	}
	var dnsNames []string
	var ips []net.IP
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			ips = append(ips, ip)
			continue
		}
		dnsNames = append(dnsNames, n)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validFor),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// agentLeafPEM issues the client certificate for a fresh, unrelated key —
// used only to simulate an identity that pre-dates the command under test
// (writeAgentCertForTest). agentLeafPEM carries the agent's URI SAN so
// InspectIdentity can read agent_id from it.
func (ca *testCA) agentLeafPEM(t *testing.T, agentID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemStr, err := ca.issueAgentCert(agentID, &key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pemStr
}

// issueAgentCert is what a real Enroll does: sign the caller's own public
// key (normally from the CSR) under the agent's URI SAN. No *testing.T here
// — it also runs from the fake gRPC server's handler goroutine.
func (ca *testCA) issueAgentCert(agentID string, pub any) (string, error) {
	uri, err := url.Parse("sard://tenants/t1/agents/" + agentID)
	if err != nil {
		return "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: agentID},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return "", err
	}
	return pemEncode("CERTIFICATE", der), nil
}

func (ca *testCA) pem() string { return pemEncode("CERTIFICATE", ca.cert.Raw) }

func pemEncode(kind string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

// chainOf is [leaf, root]: how the server presents its handshake (ADR 0014).
func chainOf(leaf tls.Certificate, root *testCA) tls.Certificate {
	leaf.Certificate = append(leaf.Certificate, root.cert.Raw)
	return leaf
}

// enrollServer answers Enroll per test, on a real TLS listener, and counts
// how many times it was actually called — the fixture behind "сервер не
// получил регистрацию" / "сервер получил регистрацию только от первой
// команды" assertions.
type enrollServer struct {
	agentv1.UnimplementedEnrollmentServiceServer
	calls  atomic.Int32
	answer func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)

	// started, when set, is closed the moment Enroll is invoked, before
	// answer runs — lets a test hold the server "mid-request".
	started chan struct{}
	// block, when set, is waited on before answer runs.
	block chan struct{}
}

func (s *enrollServer) Enroll(_ context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	s.calls.Add(1)
	if s.started != nil {
		close(s.started)
	}
	if s.block != nil {
		<-s.block
	}
	return s.answer(req)
}

func (s *enrollServer) callCount() int { return int(s.calls.Load()) }

// startFakeServer serves EnrollmentService over TLS with cert, returns the
// listen address and the server for call-count assertions.
func startFakeServer(t *testing.T, cert tls.Certificate, s *enrollServer) string {
	t.Helper()
	// 0.0.0.0, not 127.0.0.1: some tests reach this listener through a
	// different loopback address (e.g. 127.0.0.2) than the one it reports.
	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	srv := grpc.NewServer(grpc.Creds(creds))
	agentv1.RegisterEnrollmentServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return "127.0.0.1:" + port
}

// succeedingAnswer signs the CSR's own public key, like a real server
// would — a fixed, pre-baked certificate would carry a key unrelated to
// the one the command generates, and "открытый ключ сертификата совпадает
// с ключом из tls.key_file" would never hold.
func succeedingAnswer(ca *testCA, agentID string) func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	return func(req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		csr, err := x509.ParseCertificateRequest(req.GetCsrDer())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "test fixture: bad CSR: %v", err)
		}
		certPEM, err := ca.issueAgentCert(agentID, csr.PublicKey)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "test fixture: issuing the certificate failed: %v", err)
		}
		return &agentv1.EnrollResponse{AgentId: agentID, CertificateChainPem: certPEM, CaBundlePem: ca.pem()}, nil
	}
}

// host is a temporary "installed agent host": a config file and the tls.*
// directory it points at. Each of the three tls.* files lives in its own
// subdirectory so a test can make exactly one of them unwritable.
type host struct {
	dir        string
	configPath string
	keyDir     string
	certDir    string
	caDir      string
	keyFile    string
	certFile   string
	caFile     string
	address    string
}

func newHost(t *testing.T, address string) *host {
	t.Helper()
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "key")
	certDir := filepath.Join(dir, "cert")
	caDir := filepath.Join(dir, "ca")
	for _, d := range []string{keyDir, certDir, caDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h := &host{
		dir:      dir,
		keyDir:   keyDir,
		certDir:  certDir,
		caDir:    caDir,
		keyFile:  filepath.Join(keyDir, "agent.key"),
		certFile: filepath.Join(certDir, "agent.pem"),
		caFile:   filepath.Join(caDir, "ca.pem"),
		address:  address,
	}
	h.configPath = h.writeConfig(t, address)
	return h
}

func (h *host) writeConfig(t *testing.T, address string) string {
	t.Helper()
	content := fmt.Sprintf("server:\n  address: %q\ntls:\n  ca_file: %q\n  cert_file: %q\n  key_file: %q\n",
		address, h.caFile, h.certFile, h.keyFile)
	path := filepath.Join(h.dir, fmt.Sprintf("agent-%d.yaml", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newToken(t *testing.T, fingerprint string) string {
	t.Helper()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	return "sard_" + base64.RawURLEncoding.EncodeToString(secret[:]) + "." + fingerprint
}
