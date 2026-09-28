// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func TestNewIdentityKeyIsFreshP256(t *testing.T) {
	k1, err := enroll.NewIdentityKey()
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	k2, err := enroll.NewIdentityKey()
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
	key, err := enroll.NewIdentityKey()
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	der, err := enroll.BuildCSR(key, "db1")
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

func TestKeyNeverAppearsInCSRErrors(t *testing.T) {
	key, err := enroll.NewIdentityKey()
	if err != nil {
		t.Fatalf("NewIdentityKey: %v", err)
	}
	_, err = enroll.BuildCSR(key, strings.Repeat("x", 300)) // deliberately too long a CN is still fine at this layer; just exercise error text
	raw, rawErr := key.Bytes()
	if rawErr != nil {
		t.Fatal(rawErr)
	}
	if err != nil && strings.Contains(err.Error(), string(raw)) {
		t.Fatalf("error text leaks the private key: %q", err.Error())
	}
}
