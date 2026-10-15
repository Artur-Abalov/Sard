// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"

	"github.com/Artur-Abalov/sard/agent/internal/identity"
)

// responseError: the server's answer to RenewCertificate cannot be used. Its
// text is the reason RENEWAL_RESPONSE_INVALID and never quotes the answer.
type responseError struct{ detail string }

func (e *responseError) Error() string { return "RENEWAL_RESPONSE_INVALID: " + e.detail }

func invalid(detail string) error { return &responseError{detail: detail} }

// checkResponse accepts the chain only when its first certificate was issued
// on the new key, names the same agent and role as the current certificate,
// and leads to the CA of tls.ca_file (П18).
func (m *Manager) checkResponse(chain string, key *ecdsa.PrivateKey) error {
	certs := parseChain(chain)
	if len(certs) == 0 {
		return invalid("the answer holds no certificate")
	}
	if pub, ok := certs[0].PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return invalid("the certificate is not for the new key")
	}
	if err := m.checkNames(chain); err != nil {
		return err
	}
	return m.checkChain(certs)
}

func parseChain(chain string) []*x509.Certificate {
	var certs []*x509.Certificate
	rest := []byte(chain)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return certs
		}
		if cert, err := x509.ParseCertificate(block.Bytes); block.Type == "CERTIFICATE" && err == nil {
			certs = append(certs, cert)
		}
	}
}

// checkNames: the identity URI and the role URI of the answer are the current ones.
func (m *Manager) checkNames(chain string) error {
	want, err := identity.Read(m.o.Files.CertFile, m.o.FS.ReadFile)
	if err != nil {
		return err
	}
	got, err := identity.Read("the answer", func(string) ([]byte, error) { return []byte(chain), nil })
	if err != nil {
		return invalid("the certificate carries no usable identity or role")
	}
	if got.TenantID != want.TenantID || got.AgentID != want.AgentID || got.Role != want.Role {
		return invalid("the certificate names another agent or role")
	}
	return nil
}

func (m *Manager) checkChain(certs []*x509.Certificate) error {
	caPEM, err := m.o.FS.ReadFile(m.o.Files.CAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return errors.New("tls.ca_file holds no certificate")
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	opts := x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: m.o.Clock.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := certs[0].Verify(opts); err != nil {
		return invalid("the certificate chain does not lead to tls.ca_file")
	}
	return nil
}
