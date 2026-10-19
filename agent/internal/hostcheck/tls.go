// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

// The identifiers of the checks of the agent's certificate (Ч13).
const (
	CheckTLSIdentity = "tls.identity"
	CheckTLSChain    = "tls.chain"
	CheckTLSExpiry   = "tls.expiry"
)

// ReadFunc reads a file, as the service would.
type ReadFunc func(name string) ([]byte, error)

// RenewWithin is how long before the end of a certificate the check warns:
// automatic renewal (C1) is not available yet.
const RenewWithin = 30 * 24 * time.Hour

const enrollForce = "sudo sard-agent enroll --force"

// unset names the first of the keys that the config leaves empty.
func unset(keys ...struct{ name, value string }) string {
	for _, k := range keys {
		if k.value == "" {
			return k.name
		}
	}
	return ""
}

func notEnrolled(check, key, server string) Line {
	return Line{Check: check, Status: Error, Message: key + " is not set in the config: this host is not registered",
		Hint: fmt.Sprintf("sudo sard-agent enroll --server %s --token <token>", server)}
}

// TLSIdentityLine is tls.identity: the key belongs to the certificate.
func TLSIdentityLine(files config.TLS, server string, read ReadFunc) Line {
	type key = struct{ name, value string }
	if name := unset(key{"tls.cert_file", files.CertFile}, key{"tls.key_file", files.KeyFile}); name != "" {
		return notEnrolled(CheckTLSIdentity, name, server)
	}
	err := tlsid.Check(files, read)
	switch {
	case err == nil:
		return Line{Check: CheckTLSIdentity, Status: OK, Message: "the key in " + files.KeyFile + " belongs to the certificate in " + files.CertFile}
	case errors.Is(err, tlsid.ErrIncompleteEnrollment):
		return Line{Check: CheckTLSIdentity, Status: Error, Message: "tls.key_file is not the key of tls.cert_file: a registration was not finished", Hint: enrollForce}
	}
	return Line{Check: CheckTLSIdentity, Status: Error, Message: err.Error(), Hint: enrollForce}
}

// certificates reads every certificate of a PEM file, the first one first.
func certificates(read ReadFunc, path string) ([]*x509.Certificate, error) {
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	certs, err := parseCertificates(data)
	if err == nil && len(certs) == 0 {
		err = errors.New("no certificate in the file")
	}
	return certs, err
}

func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("a certificate that cannot be parsed: %w", err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

// TLSChainLine is tls.chain: the certificate was issued by a CA of
// tls.ca_file. The validity of the certificate is judged by tls.expiry, so
// the chain is verified at a moment inside it.
func TLSChainLine(files config.TLS, now time.Time, read ReadFunc) Line {
	type key = struct{ name, value string }
	if name := unset(key{"tls.cert_file", files.CertFile}, key{"tls.ca_file", files.CAFile}); name != "" {
		return Line{Check: CheckTLSChain, Status: Error, Message: name + " is not set in the config", Hint: enrollForce}
	}
	chain, err := certificates(read, files.CertFile)
	if err != nil {
		return Line{Check: CheckTLSChain, Status: Error, Message: fmt.Sprintf("%s: %v", files.CertFile, err), Hint: enrollForce}
	}
	cas, err := certificates(read, files.CAFile)
	if err != nil {
		return Line{Check: CheckTLSChain, Status: Error, Message: fmt.Sprintf("%s: %v", files.CAFile, err), Hint: enrollForce}
	}
	if err := verifyChain(chain, cas, now); err != nil {
		return Line{Check: CheckTLSChain, Status: Error, Message: fmt.Sprintf("the certificate in %s was not issued by a CA of %s: %v", files.CertFile, files.CAFile, err), Hint: enrollForce}
	}
	return Line{Check: CheckTLSChain, Status: OK, Message: "the certificate in " + files.CertFile + " was issued by a CA of " + files.CAFile}
}

// verifyChain verifies the first certificate against the CAs, with the others as intermediates.
func verifyChain(chain, cas []*x509.Certificate, now time.Time) error {
	roots, inter := x509.NewCertPool(), x509.NewCertPool()
	for _, ca := range cas {
		roots.AddCert(ca)
	}
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: within(chain[0], now), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}

// within is now, or the nearest moment the certificate is valid.
func within(c *x509.Certificate, now time.Time) time.Time {
	switch {
	case now.Before(c.NotBefore):
		return c.NotBefore
	case now.After(c.NotAfter):
		return c.NotAfter
	}
	return now
}

// TLSExpiryLine is tls.expiry: the certificate is valid now and for long
// enough (Ч13).
func TLSExpiryLine(files config.TLS, now time.Time, read ReadFunc) Line {
	if files.CertFile == "" {
		return Line{Check: CheckTLSExpiry, Status: Error, Message: "tls.cert_file is not set in the config", Hint: enrollForce}
	}
	chain, err := certificates(read, files.CertFile)
	if err != nil {
		return Line{Check: CheckTLSExpiry, Status: Error, Message: fmt.Sprintf("%s: %v", files.CertFile, err), Hint: enrollForce}
	}
	return expiryOf(chain[0], now)
}

func expiryOf(cert *x509.Certificate, now time.Time) Line {
	line := Line{Check: CheckTLSExpiry}
	left := cert.NotAfter.Sub(now)
	end := cert.NotAfter.UTC().Format("2006-01-02")
	switch {
	case now.Before(cert.NotBefore):
		line.Status = Error
		line.Message = "the certificate is not valid before " + cert.NotBefore.UTC().Format("2006-01-02 15:04 MST") + ": check the clock of this host"
		line.Hint = "see " + HelpURL(CheckTLSExpiry)
	case left <= 0:
		line.Status = Error
		line.Message = "the certificate expired on " + end
		line.Hint = enrollForce + " (see " + HelpURL(CheckTLSExpiry) + ")"
	case left < RenewWithin:
		line.Status = Warn
		line.Message = fmt.Sprintf("the certificate expires in %d days (%s); automatic renewal is not available yet (C1)", int(left/(24*time.Hour)), end)
		line.Hint = enrollForce
	default:
		line.Status = OK
		line.Message = fmt.Sprintf("the certificate is valid until %s (%d days)", end, int(left/(24*time.Hour)))
	}
	return line
}
