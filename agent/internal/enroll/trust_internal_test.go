// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

// White-box tests of trust.go's unexported helpers: TLS invokes
// VerifyPeerCertificate directly, so some of its defensive branches (a
// certificate TLS itself would never actually let through malformed, a
// second callback invocation with a verdict already cached, a
// credentials.TransportCredentials.ClientHandshake call with something
// other than the *tls.Conn DialTOFU always hands it) cannot be reached
// through a real TLS handshake in an external test; they are called here
// directly instead.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// selfSignedCert builds a self-signed certificate that is a valid CA and
// covers host, for tests that need real DER bytes but not a separate root.
func selfSignedCert(t *testing.T, host string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// TLS may invoke VerifyPeerCertificate more than once on some retries; the
// first verdict must win, not be overwritten by whatever a later call
// would have concluded on its own — including a later call that, taken on
// its own, would have succeeded.
func TestTofuVerifierKeepsItsFirstVerdict(t *testing.T) {
	cert := selfSignedCert(t, "example.com")
	v := &tofuVerifier{fingerprint: spkiFingerprint(cert), host: "example.com"}
	first := v.fail(trustError("first failure"))
	if first == nil {
		t.Fatal("fail: want a non-nil error back")
	}

	// A second call with a certificate chain that would, on its own,
	// verify successfully (matching fingerprint, matching hostname) must
	// still return the cached first verdict, not nil.
	got := v.verify([][]byte{cert.Raw, cert.Raw}, nil)
	if got != first {
		t.Fatalf("verify() after a cached verdict = %v, want the original %v unchanged", got, first)
	}
}

// A certificate TLS would never actually deliver malformed in practice
// (verify's own defense in depth) must still be reported as "could not be
// parsed", not silently treated as "no certificate" or panic.
func TestVerifyRejectsUnparsableCertificateBytes(t *testing.T) {
	v := &tofuVerifier{fingerprint: "0000000000000000000000000000000000000000000000000000000000000000"[:64], host: "example.com"}
	err := v.verify([][]byte{[]byte("not a certificate, just garbage bytes")}, nil)
	if err == nil {
		t.Fatal("verify: want an error for unparsable certificate bytes")
	}
	if !strings.Contains(err.Error(), "could not be parsed") {
		t.Fatalf("error = %q, want it to say the certificate could not be parsed", err.Error())
	}
}

// Zero certificates (defensive: a real TLS handshake always presents at
// least one) must be reported as "no certificate", not panic indexing an
// empty slice.
func TestVerifyRejectsZeroCertificates(t *testing.T) {
	v := &tofuVerifier{fingerprint: "0000000000000000000000000000000000000000000000000000000000000000"[:64], host: "example.com"}
	err := v.verify([][]byte{}, nil)
	if err == nil {
		t.Fatal("verify: want an error for zero certificates")
	}
	if !strings.Contains(err.Error(), "no certificate") {
		t.Fatalf("error = %q, want it to say no certificate was presented", err.Error())
	}
}

// parseCerts must propagate a parse failure instead of silently appending
// a nil certificate and reporting success.
func TestParseCertsPropagatesAParseFailure(t *testing.T) {
	certs, err := parseCerts([][]byte{[]byte("not a certificate")})
	if err == nil {
		t.Fatal("parseCerts: want an error for unparsable DER")
	}
	if certs != nil {
		t.Fatalf("certs = %v, want nil on failure", certs)
	}
}

// ClientHandshake must reject a net.Conn that is not the *tls.Conn DialTOFU
// always hands it, instead of proceeding to call a method on a nil
// *tls.Conn.
func TestClientHandshakeRejectsANonTLSConnection(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	_, _, err := (tofuCredentials{}).ClientHandshake(context.Background(), "", client)
	if err == nil {
		t.Fatal("ClientHandshake: want an error for a non-*tls.Conn")
	}
	if !strings.Contains(err.Error(), "expected an established TLS connection") {
		t.Fatalf("error = %q, want it to name the actual problem", err.Error())
	}
}
