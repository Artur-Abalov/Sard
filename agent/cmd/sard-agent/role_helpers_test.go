// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Fixtures of docs/specs/agent/agent-role.feature.
const (
	testTenant      = "11111111-1111-1111-1111-111111111111"
	testAgent       = "22222222-2222-2222-2222-222222222222"
	testIdentityURI = "sard://tenants/" + testTenant + "/agents/" + testAgent
	// rawMachineID is R1.
	rawMachineID = "0123456789abcdef0123456789abcdef"
	// machineIDOfR1 is hex(HMAC-SHA256("sard-machine-id/v1" + testTenant, R1)), from openssl.
	machineIDOfR1 = "b6e930a30c108211f9a4711e40469b5a8ae9e670f971d6982b789de8460790c7"
)

// roleCertNotAfter is the expiry of the certificates of rolePair.
var roleCertNotAfter = time.Date(2027, 10, 12, 12, 0, 0, 0, time.UTC)

// certOf issues a self-signed certificate on key whose SAN holds exactly
// the URI strings given, byte for byte.
func certOf(t *testing.T, key *ecdsa.PrivateKey, notAfter time.Time, uris ...string) []byte {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: testAgent}, NotAfter: notAfter}
	if len(uris) > 0 {
		var names []asn1.RawValue
		for _, u := range uris {
			names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte(u)})
		}
		san, err := asn1.Marshal(names)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: san}}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func keyPEM(t *testing.T, k *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// rolePair returns a new key and a certificate on it with the given SAN URIs.
func rolePair(t *testing.T, uris ...string) (key, cert []byte) {
	t.Helper()
	k := newKey(t)
	return keyPEM(t, k), certOf(t, k, roleCertNotAfter, uris...)
}

// installPair replaces dir/agent.pem and dir/agent.key by a pair with those URIs.
func installPair(t *testing.T, dir string, uris ...string) {
	t.Helper()
	key, cert := rolePair(t, uris...)
	writeFile(t, filepath.Join(dir, "agent.pem"), cert, 0o644)
	writeFile(t, filepath.Join(dir, "agent.key"), key, 0o600)
}

// withRole is the SAN of a certificate of the role: identity, then role URI.
func withRole(role string) []string { return []string{testIdentityURI, "sard://roles/" + role} }

// roleRefusalRows are the SAN role names (after the identity URI) that stop
// the agent, and the reason each is refused with.
var roleRefusalRows = []struct {
	names  []string
	reason string
}{
	{nil, "CERT_ROLE_MISSING"},
	{[]string{"sard://role/verifier"}, "CERT_ROLE_MISSING"},
	{[]string{"sard://roles/admin"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/Verifier"}, "CERT_ROLE_INVALID"},
	{[]string{"SARD://ROLES/verifier"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/verifier/"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/verifier?x=1"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/verifier#x"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://u@roles/verifier"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles:443/verifier"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/verifier", "sard://roles/verifier"}, "CERT_ROLE_INVALID"},
	{[]string{"sard://roles/source", "sard://roles/verifier"}, "CERT_ROLE_INVALID"},
}

// useMachineIDFiles points the agent at these machine-id files for the test
// (the host's own files are never read); "" is a path that does not exist,
// and the D-Bus one is always absent.
func useMachineIDFiles(t *testing.T, etc string) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "absent")
	old := machineIDSources
	t.Cleanup(func() { machineIDSources = old })
	machineIDSources = []string{or(etc, missing), missing}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// machineIDFile writes content as a machine-id file and returns its path.
func machineIDFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMain keeps every test off the host's own machine-id.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sard-agent-test")
	if err != nil {
		panic(err)
	}
	machineIDSources = []string{filepath.Join(dir, "etc-machine-id"), filepath.Join(dir, "dbus-machine-id")}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
