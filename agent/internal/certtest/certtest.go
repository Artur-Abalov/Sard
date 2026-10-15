// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package certtest issues the certificates the agent's tests need: a test CA
// that signs agent certificates with the identity and role URIs the server
// puts in them (docs/specs/agent/agent-role.feature), at any NotBefore and
// NotAfter, and server certificates for the fake AgentService. Test code only.
package certtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"
)

// CA signs the certificates of a test. It is valid for years around the real
// clock, so that agent clocks moved by a test stay inside its validity.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// NewCA creates a CA.
func NewCA(t testing.TB) *CA {
	t.Helper()
	key := NewKey(t)
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             now.AddDate(-1, 0, 0),
		NotAfter:              now.AddDate(10, 0, 0),
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
	return &CA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// PEM is the CA certificate, as tls.ca_file holds it.
func (ca *CA) PEM() []byte { return ca.pem }

// Server issues a TLS server certificate for the host names or addresses.
func (ca *CA) Server(t testing.TB, names ...string) tls.Certificate {
	t.Helper()
	key := NewKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// Spec is what an agent certificate says.
type Spec struct {
	Tenant, Agent, Role string
	Serial              int64
	NotBefore, NotAfter time.Time
}

// Issue signs pub as the client certificate of spec. It never fails the
// test, so a fake server may call it from a handler.
func (ca *CA) Issue(pub *ecdsa.PublicKey, spec Spec) ([]byte, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(spec.Serial),
		Subject:      pkix.Name{CommonName: spec.Agent},
		NotBefore:    spec.NotBefore,
		NotAfter:     spec.NotAfter,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{mustURL("sard://tenants/" + spec.Tenant + "/agents/" + spec.Agent)},
	}
	if spec.Role != "" {
		tmpl.URIs = append(tmpl.URIs, mustURL("sard://roles/"+spec.Role))
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

// Pair is a key and the certificate issued on it, both PEM.
type Pair struct {
	Key     *ecdsa.PrivateKey
	KeyPEM  []byte
	CertPEM []byte
}

// NewPair generates a key and issues a certificate on it.
func (ca *CA) NewPair(t testing.TB, spec Spec) Pair {
	t.Helper()
	key := NewKey(t)
	cert, err := ca.Issue(&key.PublicKey, spec)
	if err != nil {
		t.Fatal(err)
	}
	return Pair{Key: key, KeyPEM: KeyPEM(t, key), CertPEM: cert}
}

// NewKey generates an ECDSA P-256 key.
func NewKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// KeyPEM is key as a PKCS #8 PEM block.
func KeyPEM(t testing.TB, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
