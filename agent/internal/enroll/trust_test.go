// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

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

func (ca *testCA) fingerprint() string { return enrollFingerprint(ca.cert) }

// leaf issues a server certificate for names, signed by ca, expiring in
// validFor (24h if zero).
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

// chain is [leaf, root]: how the server presents its handshake (ADR 0014).
func chainOf(leaf tls.Certificate, root *testCA) tls.Certificate {
	leaf.Certificate = append(leaf.Certificate, root.cert.Raw)
	return leaf
}

func listenTLS(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	lis, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() { _ = conn.(*tls.Conn).Handshake() }() // fail the handshake on our side too if the client aborts; otherwise idle
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })
	return lis.Addr().String()
}

// listenNoTLS accepts plain TCP and answers with plaintext, like an
// ordinary HTTP service that isn't Sard's gRPC port: reading the TLS
// client's ClientHello and replying in plaintext breaks the handshake
// immediately instead of leaving it to hang forever.
func listenNoTLS(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
				_ = conn.Close()
			}()
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })
	return lis.Addr().String()
}

// listenIdleTLS accepts plain TCP and never sends anything back, so a TLS
// handshake against it only ends when its context does.
func listenIdleTLS(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			_ = conn // accept and sit idle
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })
	return lis.Addr().String()
}

type fakeEnrollmentServer struct {
	agentv1.UnimplementedEnrollmentServiceServer
}

func serveGRPC(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	creds := grpcTLSCreds(cert)
	srv := grpc.NewServer(grpc.Creds(creds))
	agentv1.RegisterEnrollmentServiceServer(srv, &fakeEnrollmentServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestDialTOFUSucceedsWhenTheFingerprintAndHostnameMatch(t *testing.T) {
	ca := newTestCA(t)
	addr := serveGRPC(t, chainOf(ca.leaf(t, []string{"localhost", "127.0.0.1"}, 0), ca))
	conn, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, ca.fingerprint())
	if err != nil {
		t.Fatalf("DialTOFU: %v", err)
	}
	defer func() { _ = conn.Close() }()
}

func TestDialTOFURejectsAMismatchedFingerprintBeforeAnyRequest(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	addr := listenTLS(t, chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca))
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, other.fingerprint())
	requireTrustClass(t, err)
}

func TestDialTOFURejectsAChainWithoutARoot(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.leaf(t, []string{"127.0.0.1"}, 0) // no root appended
	addr := listenTLS(t, leaf)
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, ca.fingerprint())
	requireTrustClass(t, err)
}

func TestDialTOFURejectsALeafNotSignedByThePinnedRoot(t *testing.T) {
	ca := newTestCA(t)
	signer := newTestCA(t)
	leaf := signer.leaf(t, []string{"127.0.0.1"}, 0)
	leaf.Certificate = append(leaf.Certificate, ca.cert.Raw) // fingerprint matches ca, signature doesn't
	addr := listenTLS(t, leaf)
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, ca.fingerprint())
	requireTrustClass(t, err)
}

func TestDialTOFURejectsAnExpiredServerCertificate(t *testing.T) {
	ca := newTestCA(t)
	addr := listenTLS(t, chainOf(ca.leaf(t, []string{"127.0.0.1"}, -time.Hour), ca))
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, ca.fingerprint())
	requireTrustClass(t, err)
}

func TestDialTOFURejectsAHostnameNotInTheCertificateAndListsTheCertNames(t *testing.T) {
	ca := newTestCA(t)
	addr := listenTLS(t, chainOf(ca.leaf(t, []string{"sard.example.com", "localhost"}, 0), ca))
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, ca.fingerprint())
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTrust {
		t.Fatalf("class = %q, want trust", eerr.Class)
	}
	if len(eerr.Names) == 0 {
		t.Fatal("want the server certificate names in the error")
	}
}

func TestDialTOFUClassifiesAServerWithoutTLSAsTrust(t *testing.T) {
	addr := listenNoTLS(t)
	_, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, "0000000000000000000000000000000000000000000000000000000000000000"[:64])
	requireTrustClass(t, err)
}

func TestDialTOFUClassifiesAnUnreachableServerAsTemporaryAndNamesTheAddress(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close() // nobody listens now

	_, derr := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, "0000000000000000000000000000000000000000000000000000000000000000"[:64])
	var eerr *enroll.Error
	if !errors.As(derr, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", derr)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
	if eerr.Address != addr {
		t.Fatalf("address = %q, want %q", eerr.Address, addr)
	}
}

func TestDialTOFUClassifiesAContextDeadlineAsTemporary(t *testing.T) {
	addr := listenIdleTLS(t) // accepts and sits idle, so the dial itself succeeds
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := enroll.DialTOFU(ctx, enroll.RealDial, addr, "0000000000000000000000000000000000000000000000000000000000000000"[:64])
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
}

// intermediateCA issues an intermediate CA certificate signed by ca — used
// to prove leaf.Verify (F13) builds the chain through it, not just from
// the leaf straight to the pinned root.
func (ca *testCA) intermediateCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test intermediate CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

// F13: the chain presented in the handshake may carry an intermediate
// between the leaf and the pinned root; leaf.Verify must be given it as an
// Intermediate, not just the pinned root, or a perfectly valid chain fails.
func TestDialTOFUAcceptsAChainWithAnIntermediateBetweenLeafAndThePinnedRoot(t *testing.T) {
	root := newTestCA(t)
	intermediate := root.intermediateCA(t)
	leaf := intermediate.leaf(t, []string{"127.0.0.1"}, 0)
	leaf.Certificate = append(leaf.Certificate, intermediate.cert.Raw, root.cert.Raw)
	addr := serveGRPC(t, leaf)
	conn, err := enroll.DialTOFU(context.Background(), enroll.RealDial, addr, root.fingerprint())
	if err != nil {
		t.Fatalf("DialTOFU: %v", err)
	}
	defer func() { _ = conn.Close() }()
}

func requireTrustClass(t *testing.T, err error) {
	t.Helper()
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTrust {
		t.Fatalf("class = %q, want trust", eerr.Class)
	}
}
