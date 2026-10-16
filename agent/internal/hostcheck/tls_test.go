// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rules "Каждая ошибка и предупреждение check сопровождаются подсказкой" and
// "Сертификат на пороге 30 дней" (Ч13): tls.identity, tls.chain, tls.expiry.

var tlsNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// pki is a fake CA, an agent certificate it issued and the key of that
// certificate, as the files enroll writes.
type pki struct {
	caPEM, certPEM, keyPEM []byte
}

func newCA(t *testing.T, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: tlsNow.Add(-1000 * 24 * time.Hour), NotAfter: tlsNow.Add(1000 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func issue(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, notBefore, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "agent"},
		NotBefore: notBefore, NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func newPKI(t *testing.T, notBefore, notAfter time.Time) pki {
	t.Helper()
	ca, caKey := newCA(t, "sard-ca")
	cert, key := issue(t, ca, caKey, notBefore, notAfter)
	return pki{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), certPEM: cert, keyPEM: key}
}

func validPKI(t *testing.T, left time.Duration) pki {
	t.Helper()
	return newPKI(t, tlsNow.Add(-24*time.Hour), tlsNow.Add(left))
}

var tlsFiles = config.TLS{CAFile: "/tls/ca.pem", CertFile: "/tls/agent.pem", KeyFile: "/tls/agent.key"}

func (p pki) read(name string) ([]byte, error) {
	switch name {
	case "/tls/ca.pem":
		return p.caPEM, nil
	case "/tls/agent.pem":
		return p.certPEM, nil
	case "/tls/agent.key":
		return p.keyPEM, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func TestTheKeyOfTheCertificateIsOK(t *testing.T) {
	l := hostcheck.TLSIdentityLine(tlsFiles, "sard.example:9443", validPKI(t, 200*24*time.Hour).read)
	if l.Check != "tls.identity" || l.Status != hostcheck.OK || l.Hint != "" {
		t.Errorf("%+v", l)
	}
}

func TestAKeyOfAnotherCertificateAsksForEnrollForce(t *testing.T) {
	good, other := validPKI(t, 200*24*time.Hour), validPKI(t, 200*24*time.Hour)
	l := hostcheck.TLSIdentityLine(tlsFiles, "sard.example:9443", func(name string) ([]byte, error) {
		if name == "/tls/agent.key" {
			return other.keyPEM, nil
		}
		return good.read(name)
	})
	if l.Status != hostcheck.Error || !strings.Contains(l.Hint, "sudo sard-agent enroll --force") {
		t.Errorf("%+v", l)
	}
}

func TestWithoutACertificateInTheConfigTheHintIsEnrollWithTheServerAddress(t *testing.T) {
	files := tlsFiles
	files.CertFile = ""
	l := hostcheck.TLSIdentityLine(files, "sard.example:9443", validPKI(t, time.Hour).read)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "tls.cert_file") ||
		!strings.Contains(l.Hint, "sudo sard-agent enroll --server sard.example:9443 --token") {
		t.Errorf("%+v", l)
	}
}

func TestAMissingFileOfTheIdentityIsAnErrorThatNamesIt(t *testing.T) {
	l := hostcheck.TLSIdentityLine(tlsFiles, "s:1", func(string) ([]byte, error) { return nil, fs.ErrNotExist })
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "/tls/agent.pem") || !strings.Contains(l.Hint, "enroll --force") {
		t.Errorf("%+v", l)
	}
}

func TestACertificateOfTheCAOfTheFileIsOK(t *testing.T) {
	l := hostcheck.TLSChainLine(tlsFiles, tlsNow, validPKI(t, 200*24*time.Hour).read)
	if l.Check != "tls.chain" || l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestACertificateOfAnotherCAIsAnError(t *testing.T) {
	good, other := validPKI(t, 200*24*time.Hour), validPKI(t, 200*24*time.Hour)
	l := hostcheck.TLSChainLine(tlsFiles, tlsNow, func(name string) ([]byte, error) {
		if name == "/tls/ca.pem" {
			return other.caPEM, nil
		}
		return good.read(name)
	})
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "not issued by") || !strings.Contains(l.Hint, "sudo sard-agent enroll --force") {
		t.Errorf("%+v", l)
	}
}

func TestTheChainOfAnExpiredCertificateIsJudgedWithoutItsExpiry(t *testing.T) {
	expired := newPKI(t, tlsNow.Add(-90*24*time.Hour), tlsNow.Add(-24*time.Hour))
	if l := hostcheck.TLSChainLine(tlsFiles, tlsNow, expired.read); l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestACAFileThatIsNotACertificateIsAnError(t *testing.T) {
	good := validPKI(t, time.Hour)
	l := hostcheck.TLSChainLine(tlsFiles, tlsNow, func(name string) ([]byte, error) {
		if name == "/tls/ca.pem" {
			return []byte("garbage"), nil
		}
		return good.read(name)
	})
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "/tls/ca.pem") {
		t.Errorf("%+v", l)
	}
	l = hostcheck.TLSChainLine(tlsFiles, tlsNow, func(string) ([]byte, error) { return nil, errors.New("boom") })
	if l.Status != hostcheck.Error {
		t.Errorf("%+v", l)
	}
}

func TestExpiryByTheNumberOfDaysLeft(t *testing.T) {
	for _, c := range []struct {
		name   string
		left   time.Duration
		status hostcheck.Status
		in     []string
	}{
		{"200 days", 200 * 24 * time.Hour, hostcheck.OK, []string{"2027-05-02"}},
		{"exactly 30 days", 30 * 24 * time.Hour, hostcheck.OK, []string{"2026-11-13"}},
		{"just under 30 days", 30*24*time.Hour - time.Second, hostcheck.Warn, []string{"expires in 29 days"}},
		{"10 days", 10 * 24 * time.Hour, hostcheck.Warn, []string{"expires in 10 days", "automatic renewal is not available yet"}},
	} {
		l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, validPKI(t, c.left).read)
		if l.Check != "tls.expiry" || l.Status != c.status {
			t.Errorf("%s: %+v", c.name, l)
		}
		for _, want := range c.in {
			if !strings.Contains(l.Message, want) {
				t.Errorf("%s: message %q lacks %q", c.name, l.Message, want)
			}
		}
		if c.status == hostcheck.Warn && !strings.Contains(l.Hint, "sudo sard-agent enroll --force") {
			t.Errorf("%s: hint %q", c.name, l.Hint)
		}
	}
}

func TestAnExpiredCertificateAsksForANewRegistration(t *testing.T) {
	p := newPKI(t, tlsNow.Add(-90*24*time.Hour), tlsNow.Add(-24*time.Hour))
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, p.read)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "expired") || !strings.Contains(l.Hint, "sudo sard-agent enroll --force") {
		t.Errorf("%+v", l)
	}
}

func TestACertificateNotYetValidPointsAtTheGuideAndTheClock(t *testing.T) {
	p := newPKI(t, tlsNow.Add(24*time.Hour), tlsNow.Add(100*24*time.Hour))
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, p.read)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "not valid before") ||
		!strings.Contains(l.Hint, "09-troubleshooting.md#tlsexpiry") || strings.Contains(l.Hint, "enroll") {
		t.Errorf("%+v", l)
	}
}

func TestTheExpiryOfAMissingCertificateIsAnError(t *testing.T) {
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, func(string) ([]byte, error) { return nil, fs.ErrNotExist })
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "/tls/agent.pem") {
		t.Errorf("%+v", l)
	}
	files := tlsFiles
	files.CertFile = ""
	if l := hostcheck.TLSExpiryLine(files, tlsNow, nil); l.Status != hostcheck.Error || !strings.Contains(l.Message, "tls.cert_file") {
		t.Errorf("%+v", l)
	}
}
