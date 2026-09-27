// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build ignore

// Regenerates the agent CSR fixtures the way sard-agent will create them
// (Go crypto/x509), so the Kotlin CA is tested against real Go output.
//
//	cd server/src/test/resources/pki && go run gencsr.go
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"log"
	"os"
)

func csr(key crypto.Signer) []byte {
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored-by-the-ca"}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		log.Fatal(err)
	}
	return der
}

func write(name string, der []byte) {
	if err := os.WriteFile(name, der, 0o644); err != nil {
		log.Fatal(err)
	}
}

func main() {
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p521, _ := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)

	write("agent-p256.csr", csr(p256))
	write("agent-p384.csr", csr(p384))
	write("agent-p521.csr", csr(p521)) // ECDSA, but a curve the CA does not accept
	write("agent-rsa.csr", csr(rsaKey))
	write("agent-ed25519.csr", csr(edKey))

	// The last byte belongs to the ECDSA signature: the DER stays well-formed,
	// the signature no longer verifies.
	bad := csr(p256)
	bad[len(bad)-1] ^= 0xff
	write("agent-bad-signature.csr", bad)
}
