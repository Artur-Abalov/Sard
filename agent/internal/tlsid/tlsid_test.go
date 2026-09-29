// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package tlsid_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

const message = "tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`"

// pair is a key and a certificate issued on it, both PEM.
type pair struct{ key, cert []byte }

func newPair(t *testing.T, signer crypto.Signer) pair {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	return pair{
		key:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var files = config.TLS{CAFile: "/etc/sard/ca.pem", CertFile: "/etc/sard/agent.pem", KeyFile: "/etc/sard/agent.key"}

func check(cert, key []byte) error {
	disk := map[string][]byte{files.CertFile: cert, files.KeyFile: key}
	return tlsid.Check(files, func(name string) ([]byte, error) {
		data, ok := disk[name]
		if !ok || data == nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return data, nil
	})
}

func TestAMatchingPairPasses(t *testing.T) {
	a := newPair(t, ecKey(t))
	if err := check(a.cert, a.key); err != nil {
		t.Fatal(err)
	}
	ca := newPair(t, ecKey(t))
	if err := check(append(a.cert, ca.cert...), a.key); err != nil {
		t.Errorf("chain after the agent certificate: %v", err)
	}
}

func TestSEC1AndPKCS1KeysAreRead(t *testing.T) {
	ec := ecKey(t)
	a := newPair(t, ec)
	der, _ := x509.MarshalECPrivateKey(ec)
	if err := check(a.cert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		t.Errorf("EC PRIVATE KEY: %v", err)
	}
	r, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b := newPair(t, r)
	if err := check(b.cert, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(r)})); err != nil {
		t.Errorf("RSA PRIVATE KEY: %v", err)
	}
}

func TestAKeyOfAnotherPairIsAnIncompleteEnrollment(t *testing.T) {
	a, b := newPair(t, ecKey(t)), newPair(t, ecKey(t))
	r, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string][]byte{"another P-256 key": b.key, "an RSA key": newPair(t, r).key} {
		err := check(a.cert, key)
		if !errors.Is(err, tlsid.ErrIncompleteEnrollment) || err.Error() != message {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Only the first certificate counts.
	if err := check(append(b.cert, a.cert...), a.key); !errors.Is(err, tlsid.ErrIncompleteEnrollment) {
		t.Errorf("second certificate matches: err = %v", err)
	}
}

func TestFileProblemsNameTheKeyAndPathOnly(t *testing.T) {
	a := newPair(t, ecKey(t))
	const marker = "MARKER-S"
	garbage := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(marker)})
	badKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(marker)})
	for name, tc := range map[string]struct {
		cert, key []byte
		confKey   string
	}{
		"cert missing":        {nil, a.key, "tls.cert_file"},
		"cert empty":          {[]byte{}, a.key, "tls.cert_file"},
		"cert without PEM":    {[]byte(marker), a.key, "tls.cert_file"},
		"cert does not parse": {garbage, a.key, "tls.cert_file"},
		"cert holds a key":    {a.key, a.key, "tls.cert_file"},
		"key missing":         {a.cert, nil, "tls.key_file"},
		"key empty":           {a.cert, []byte{}, "tls.key_file"},
		"key without PEM":     {a.cert, []byte(marker), "tls.key_file"},
		"key does not parse":  {a.cert, badKey, "tls.key_file"},
		"key holds a cert":    {a.cert, a.cert, "tls.key_file"},
		"both missing":        {nil, nil, "tls.cert_file"},
	} {
		requireFileError(t, name, check(tc.cert, tc.key), tc.confKey)
	}
}

func requireFileError(t *testing.T, name string, err error, confKey string) {
	t.Helper()
	path := map[string]string{"tls.cert_file": files.CertFile, "tls.key_file": files.KeyFile}[confKey]
	var fileErr *tlsid.FileError
	if !errors.As(err, &fileErr) || fileErr.Key != confKey || fileErr.Path != path || errors.Is(err, tlsid.ErrIncompleteEnrollment) {
		t.Errorf("%s: err = %v", name, err)
		return
	}
	requireOnlyKeyAndPath(t, name, err.Error(), confKey+" "+`"`+path+`"`)
}

func requireOnlyKeyAndPath(t *testing.T, name, msg, prefix string) {
	t.Helper()
	if !strings.HasPrefix(msg, prefix) || strings.Contains(msg, "MARKER-S") || strings.Contains(msg, "PRIVATE KEY") {
		t.Errorf("%s: message %q", name, msg)
	}
}

func TestWithoutCertOrKeyPathsThereIsNothingToCheck(t *testing.T) {
	if err := tlsid.Check(config.TLS{}, nil); err != nil {
		t.Fatal(err)
	}
}
