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

// Scenario "Строка tls.expiry судит по доле срока сертификата" (П46): the
// certificate lives 300 days from T0.
func TestExpiryByTheShareOfTheLifetimeLeft(t *testing.T) {
	const day = 24 * time.Hour
	start := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name    string
		elapsed time.Duration
		status  hostcheck.Status
		message string
		hint    string
	}{
		{"25 days left of 300", 275 * day, hostcheck.OK, "the certificate is valid until 2027-08-11 (25 days)", ""},
		{"24 days left of 300", 276 * day, hostcheck.Warn,
			"the certificate expires in 24 days (2027-08-11) and has not been renewed: see renewal_last_error in sudo sard-agent status",
			"sudo sard-agent status"},
		{"expired", 300 * day, hostcheck.Error, "the certificate expired on 2027-08-11", "sudo sard-agent enroll --force (see "},
	} {
		p := newPKI(t, start, start.Add(300*day))
		l := hostcheck.TLSExpiryLine(tlsFiles, start.Add(c.elapsed), p.read)
		if l.Check != "tls.expiry" || l.Status != c.status || l.Message != c.message {
			t.Errorf("%s: %+v", c.name, l)
		}
		if !strings.HasPrefix(l.Hint, c.hint) || (c.hint == "" && l.Hint != "") {
			t.Errorf("%s: hint %q", c.name, l.Hint)
		}
	}
}

// The threshold is exactly 30/365 of the lifetime, to the nanosecond.
func TestTheWarningStartsAtExactly30In365OfTheLifetime(t *testing.T) {
	const day = 24 * time.Hour
	p := newPKI(t, tlsNow, tlsNow.Add(365*day))
	end := tlsNow.Add(365 * day)
	if l := hostcheck.TLSExpiryLine(tlsFiles, end.Add(-30*day), p.read); l.Status != hostcheck.Warn {
		t.Errorf("at 30 days: %+v", l)
	}
	if l := hostcheck.TLSExpiryLine(tlsFiles, end.Add(-30*day-time.Nanosecond), p.read); l.Status != hostcheck.OK {
		t.Errorf("a nanosecond before: %+v", l)
	}
}

func TestAShortCertificateWarnsInProportion(t *testing.T) {
	p := newPKI(t, tlsNow, tlsNow.Add(10*time.Minute))
	if l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow.Add(9*time.Minute+10*time.Second), p.read); l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
	if l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow.Add(9*time.Minute+11*time.Second), p.read); l.Status != hostcheck.Warn {
		t.Errorf("%+v", l)
	}
}

func TestTheExpiryTextNoLongerSaysThereIsNoRenewal(t *testing.T) {
	p := newPKI(t, tlsNow.Add(-362*24*time.Hour), tlsNow.Add(3*24*time.Hour))
	if l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, p.read); strings.Contains(l.Message, "automatic renewal is not available yet") {
		t.Errorf("%+v", l)
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
