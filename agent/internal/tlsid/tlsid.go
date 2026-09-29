// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package tlsid checks at startup that the agent's key belongs to its
// certificate (OQ-027): `enroll --force` interrupted between the two
// renames leaves a new key next to the old certificate.
package tlsid

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// ErrIncompleteEnrollment means tls.key_file is not the key of the first
// certificate in tls.cert_file. The text is what the operator sees
// (docs/specs/agent/agent-tls-identity.feature).
var ErrIncompleteEnrollment = errors.New("tls.key_file не соответствует tls.cert_file: регистрация не завершена — повторите `sard-agent enroll --force`")

// FileError is a missing, unreadable or unparsable tls file. It names the
// config key and the path, never the content.
type FileError struct {
	Key, Path string
	Err       error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s %q: %v", e.Key, e.Path, e.Err) }

func (e *FileError) Unwrap() error { return e.Err }

var (
	errNoCertificate = errors.New("no parsable CERTIFICATE PEM block")
	errNoKey         = errors.New("no parsable private key PEM block")
)

// Check compares the public key of the first certificate in tls.cert_file
// with the private key in tls.key_file; the certificate is read first.
// Without both paths there is nothing to check: the transport reports it.
func Check(files config.TLS, read func(name string) ([]byte, error)) error {
	if files.CertFile == "" || files.KeyFile == "" {
		return nil
	}
	return checkPair(files, read)
}

func checkPair(files config.TLS, read func(name string) ([]byte, error)) error {
	cert, err := readCertificate(files.CertFile, read)
	if err != nil {
		return err
	}
	key, err := readKey(files.KeyFile, read)
	if err != nil {
		return err
	}
	if !belongs(key, cert) {
		return ErrIncompleteEnrollment
	}
	return nil
}

// belongs reports whether key is the private key of cert.
func belongs(key crypto.Signer, cert *x509.Certificate) bool {
	pub, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })
	return ok && pub.Equal(cert.PublicKey)
}

func readCertificate(path string, read func(string) ([]byte, error)) (*x509.Certificate, error) {
	fail := func(err error) (*x509.Certificate, error) {
		return nil, &FileError{Key: "tls.cert_file", Path: path, Err: err}
	}
	data, err := read(path)
	if err != nil {
		return fail(err)
	}
	block := firstBlock(data, "CERTIFICATE")
	if block == nil {
		return fail(errNoCertificate)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fail(errNoCertificate)
	}
	return cert, nil
}

func readKey(path string, read func(string) ([]byte, error)) (crypto.Signer, error) {
	fail := func(err error) (crypto.Signer, error) {
		return nil, &FileError{Key: "tls.key_file", Path: path, Err: err}
	}
	data, err := read(path)
	if err != nil {
		return fail(err)
	}
	block := firstBlock(data, "PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY")
	if block == nil {
		return fail(errNoKey)
	}
	key, ok := parseKey(block)
	if !ok {
		return fail(errNoKey)
	}
	return key, nil
}

// parseKey reads PKCS #8, SEC 1 or PKCS #1; parser errors are dropped, as
// they could quote key bytes.
func parseKey(block *pem.Block) (crypto.Signer, bool) {
	var key any
	var err error
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	signer, ok := key.(crypto.Signer)
	return signer, err == nil && ok
}

// firstBlock returns the first PEM block of one of the types.
func firstBlock(data []byte, types ...string) *pem.Block {
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			return nil
		}
		for _, t := range types {
			if block.Type == t {
				return block
			}
		}
		data = rest
	}
}
