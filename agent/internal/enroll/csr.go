// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
)

// NewIdentityKey generates a fresh ECDSA P-256 key for a new agent
// identity, drawing randomness from random (crypto/rand.Reader in
// production). The server's CA also accepts P-384, but the agent always
// generates P-256 (ADR 0014).
func NewIdentityKey(random io.Reader) (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		// ecdsa.GenerateKey's error never carries key material.
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return key, nil
}

// BuildCSR builds a DER-encoded PKCS#10 certificate signing request for
// key, naming hostname as its subject CommonName. The server ignores
// subject and extensions and builds the certificate itself (ADR 0014); the
// CommonName only lets a human read the request in transit.
func BuildCSR(random io.Reader, key *ecdsa.PrivateKey, hostname string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: hostname},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificateRequest(random, tmpl, key)
	if err != nil {
		// x509.CreateCertificateRequest's error text does not include the key.
		return nil, fmt.Errorf("create CSR: %w", err)
	}
	return der, nil
}
