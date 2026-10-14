// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	rand "crypto/rand"
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// failingReader always fails; passing it as the entropy source forces
// the (otherwise practically unreachable) error branches of
// ecdsa.GenerateKey and x509.CreateCertificateRequest.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("simulated entropy failure") }

// enableCustomRand makes ecdsa.GenerateKey and x509.CreateCertificateRequest
// honour the supplied io.Reader: since Go 1.26 they ignore it unless
// GODEBUG=cryptocustomrand=1 (go doc crypto/ecdsa.GenerateKey), which is what
// makes their error branches reachable here.
func enableCustomRand(t *testing.T) {
	t.Helper()
	t.Setenv("GODEBUG", "cryptocustomrand=1")
}

func TestNewIdentityKeyIsFreshP256(t *testing.T) {
	k1, err := enroll.NewIdentityKey(rand.Reader)
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	k2, err := enroll.NewIdentityKey(rand.Reader)
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	if k1.Curve != elliptic.P256() {
		t.Fatalf("curve = %v, want P-256", k1.Curve)
	}
	if k1.Equal(k2) {
		t.Fatal("two calls returned the same key")
	}
}

func TestBuildCSRCarriesTheHostname(t *testing.T) {
	key, err := enroll.NewIdentityKey(rand.Reader)
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	der, err := enroll.BuildCSR(rand.Reader, key, "db1")
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatalf("CheckSignature: %v", err)
	}
	if csr.Subject.CommonName != "db1" {
		t.Fatalf("CommonName = %q, want db1", csr.Subject.CommonName)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		t.Fatal("CSR public key does not match the generated key")
	}
}

// NewIdentityKey must propagate a key-generation failure, wrapped with
// context and without a nil key masquerading as success.
func TestNewIdentityKeyPropagatesAKeyGenerationFailure(t *testing.T) {
	enableCustomRand(t)
	key, err := enroll.NewIdentityKey(failingReader{})
	if err == nil {
		t.Fatal("NewIdentityKey: want an error when the entropy source fails")
	}
	if key != nil {
		t.Fatalf("key = %v, want nil on failure", key)
	}
	if !strings.Contains(err.Error(), "generate key") {
		t.Fatalf("error = %q, want it to name the failing step", err.Error())
	}
}

// BuildCSR must propagate a CSR-generation failure the same way.
func TestBuildCSRPropagatesACSRGenerationFailure(t *testing.T) {
	key, err := enroll.NewIdentityKey(rand.Reader)
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	enableCustomRand(t)
	der, err := enroll.BuildCSR(failingReader{}, key, "db1")
	if err == nil {
		t.Fatal("BuildCSR: want an error when the entropy source fails")
	}
	if der != nil {
		t.Fatalf("der = %v, want nil on failure", der)
	}
	if !strings.Contains(err.Error(), "create CSR") {
		t.Fatalf("error = %q, want it to name the failing step", err.Error())
	}
}

func TestKeyNeverAppearsInCSRErrors(t *testing.T) {
	key, err := enroll.NewIdentityKey(rand.Reader)
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	_, err = enroll.BuildCSR(rand.Reader, key, strings.Repeat("x", 300)) // deliberately too long a CN is still fine at this layer; just exercise error text
	raw, rawErr := key.Bytes()
	if rawErr != nil {
		t.Fatal(rawErr)
	}
	if err != nil && strings.Contains(err.Error(), string(raw)) {
		t.Fatalf("error text leaks the private key: %q", err.Error())
	}
}
