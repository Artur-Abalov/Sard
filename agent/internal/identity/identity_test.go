// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package identity_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/identity"
	"github.com/Artur-Abalov/sard/agent/internal/role"
)

const (
	tenant   = "11111111-1111-1111-1111-111111111111"
	agent    = "22222222-2222-2222-2222-222222222222"
	identURI = "sard://tenants/" + tenant + "/agents/" + agent
)

var notAfter = time.Date(2027, 10, 12, 12, 0, 0, 0, time.UTC)

// certPEM issues a certificate whose SAN carries exactly the URI strings
// given, byte for byte (x509.CreateCertificate would normalise them).
func certPEM(t *testing.T, uris ...string) []byte {
	t.Helper()
	var names []asn1.RawValue
	for _, u := range uris {
		names = append(names, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte(u)})
	}
	return certPEMNames(t, names...)
}

// certPEMNames issues a certificate whose SAN carries exactly the names given.
func certPEMNames(t *testing.T, names ...asn1.RawValue) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	san, err := asn1.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(0xabcdef12), // hex letters: the base of the text matters
		Subject:         pkix.Name{CommonName: agent},
		NotBefore:       notAfter.Add(-time.Hour),
		NotAfter:        notAfter,
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: san}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func readOf(data []byte) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return data, nil }
}

func TestReadsTheIdentityAndRoleOfACertificate(t *testing.T) {
	for _, role := range []role.Role{role.Source, role.Verifier} {
		got, err := identity.Read("/a.pem", readOf(certPEM(t, identURI, "sard://roles/"+string(role))))
		if err != nil {
			t.Fatal(err)
		}
		want := identity.Identity{TenantID: tenant, AgentID: agent, Role: role, NotAfter: notAfter, NotBefore: notAfter.Add(-time.Hour), Serial: "abcdef12"}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestTheOrderOfNamesInTheSANDoesNotMatter(t *testing.T) {
	got, err := identity.Read("/a.pem", readOf(certPEM(t, "sard://roles/verifier", identURI)))
	if err != nil || got.Role != role.Verifier || got.AgentID != agent || got.TenantID != tenant {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestACertificateWithoutAValidRoleIsRefusedWithTheReason(t *testing.T) {
	rows := []struct {
		name   string
		extra  []string
		reason string
	}{
		{"no names", nil, "CERT_ROLE_MISSING"},
		{"role instead of roles", []string{"sard://role/verifier"}, "CERT_ROLE_MISSING"},
		{"other scheme", []string{"https://roles/verifier"}, "CERT_ROLE_MISSING"},
		{"admin", []string{"sard://roles/admin"}, "CERT_ROLE_INVALID"},
		{"capital V", []string{"sard://roles/Verifier"}, "CERT_ROLE_INVALID"},
		{"upper case scheme and host", []string{"SARD://ROLES/verifier"}, "CERT_ROLE_INVALID"},
		{"empty", []string{"sard://roles/"}, "CERT_ROLE_INVALID"},
		{"trailing slash", []string{"sard://roles/verifier/"}, "CERT_ROLE_INVALID"},
		{"query", []string{"sard://roles/verifier?x=1"}, "CERT_ROLE_INVALID"},
		{"fragment", []string{"sard://roles/verifier#x"}, "CERT_ROLE_INVALID"},
		{"empty fragment", []string{"sard://roles/verifier#"}, "CERT_ROLE_INVALID"},
		{"userinfo", []string{"sard://u@roles/verifier"}, "CERT_ROLE_INVALID"},
		{"port", []string{"sard://roles:443/verifier"}, "CERT_ROLE_INVALID"},
		{"twice", []string{"sard://roles/verifier", "sard://roles/verifier"}, "CERT_ROLE_INVALID"},
		{"both roles", []string{"sard://roles/source", "sard://roles/verifier"}, "CERT_ROLE_INVALID"},
	}
	for _, row := range rows {
		_, err := identity.Read("/etc/sard/agent.pem", readOf(certPEM(t, append([]string{identURI}, row.extra...)...)))
		var re *identity.RoleError
		if !errors.As(err, &re) {
			t.Errorf("%s: err = %v", row.name, err)
			continue
		}
		for _, want := range []string{row.reason, "tls.cert_file", "/etc/sard/agent.pem", "sard-agent enroll --force"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q lacks %q", row.name, err, want)
			}
		}
		if strings.Contains(err.Error(), "CERT_ROLE_"+map[string]string{"CERT_ROLE_MISSING": "INVALID", "CERT_ROLE_INVALID": "MISSING"}[row.reason]) {
			t.Errorf("%s: %q names the other reason", row.name, err)
		}
	}
}

func TestTheRoleValueNeverReachesTheMessage(t *testing.T) {
	_, err := identity.Read("/a.pem", readOf(certPEM(t, identURI, "sard://roles/marker-ROLE-7f")))
	if err == nil || strings.Contains(err.Error(), "marker-ROLE-7f") {
		t.Fatalf("err = %v", err)
	}
}

func TestOnlyTheFirstCertificateOfTheFileCounts(t *testing.T) {
	data := append(certPEM(t, identURI), certPEM(t, identURI, "sard://roles/source")...)
	_, err := identity.Read("/a.pem", readOf(data))
	var re *identity.RoleError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "CERT_ROLE_MISSING") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnusableCertificateFileIsNamedWithoutItsContent(t *testing.T) {
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("MARKER-BODY")})
	rows := map[string][]byte{
		"empty":            {},
		"not a pem":        []byte("MARKER-S"),
		"unparsable":       bad,
		"no identity":      certPEM(t, "sard://roles/source"),
		"foreign identity": certPEM(t, "sard://tenants/t/agents/a/extra", "sard://roles/source"),
	}
	for name, data := range rows {
		_, err := identity.Read("/etc/sard/agent.pem", readOf(data))
		var re *identity.RoleError
		if err == nil || errors.As(err, &re) {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		for _, want := range []string{"tls.cert_file", `"/etc/sard/agent.pem"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q lacks %q", name, err, want)
			}
		}
		if strings.Contains(err.Error(), "MARKER") || strings.Contains(err.Error(), "CERTIFICATE") {
			t.Errorf("%s: %q leaks the content", name, err)
		}
	}
}

func TestAReadErrorKeepsItsCauseForTheCaller(t *testing.T) {
	_, err := identity.Read("/a.pem", func(string) ([]byte, error) { return nil, os.ErrNotExist })
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

// vectors are the role URI rows shared with the server: test/vectors/agent-role-uri.tsv.
func vectors(t *testing.T) [][2]string {
	t.Helper()
	data, err := os.ReadFile("../../../test/vectors/agent-role-uri.tsv")
	if err != nil {
		t.Fatal(err)
	}
	var rows [][2]string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		uri, outcome, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad vector %q", line)
		}
		rows = append(rows, [2]string{uri, outcome})
	}
	if len(rows) == 0 {
		t.Fatal("no vectors")
	}
	return rows
}

func TestTheSharedRoleURIVectors(t *testing.T) {
	for _, row := range vectors(t) {
		uri, want := row[0], row[1]
		got, err := identity.Read("/a.pem", readOf(certPEM(t, identURI, uri)))
		var re *identity.RoleError
		switch want {
		case "source", "verifier":
			if err != nil || got.Role != role.Role(want) {
				t.Errorf("%s: got %q, %v", uri, got.Role, err)
			}
		case "missing", "invalid":
			if !errors.As(err, &re) || re.Missing != (want == "missing") {
				t.Errorf("%s: err = %v, want %s", uri, err, want)
			}
		default:
			t.Fatalf("unknown outcome %q", want)
		}
	}
}

// Only a uniformResourceIdentifier name (context-specific tag 6) can carry
// the role: the same text in another kind of SAN name is no role.
func TestARoleURIInANameOfAnotherKindIsNoRole(t *testing.T) {
	roleText := []byte("sard://roles/source")
	rows := []struct {
		name string
		raw  asn1.RawValue
	}{
		{"dNSName", asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: roleText}},
		{"rfc822Name", asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, Bytes: roleText}},
		{"application class, tag 6", asn1.RawValue{Class: asn1.ClassApplication, Tag: 6, Bytes: roleText}},
	}
	ident := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte(identURI)}
	for _, row := range rows {
		_, err := identity.Read("/a.pem", readOf(certPEMNames(t, ident, row.raw)))
		var re *identity.RoleError
		if !errors.As(err, &re) || !re.Missing {
			t.Errorf("%s: err = %v", row.name, err)
		}
	}
}
