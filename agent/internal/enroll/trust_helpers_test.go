// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"

	"google.golang.org/grpc/credentials"
)

// enrollFingerprint mirrors ADR 0014: SHA-256 of the DER SubjectPublicKeyInfo.
func enrollFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

func grpcTLSCreds(cert tls.Certificate) credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
}
