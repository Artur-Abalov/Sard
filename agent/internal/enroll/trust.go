// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// DialTOFU connects to address, trusting on first use (ADR 0014, D4.3):
// the only accepted root is whichever certificate in the presented TLS
// chain has a SHA-256 SPKI fingerprint equal to fingerprint. The server's
// leaf certificate must chain to that root and cover the host in address.
// This check runs to completion, over a raw TLS handshake, before the
// returned connection is handed to anything that would send the
// enrollment token or a CSR: a rejected server never receives either.
//
// The returned error is always a *Error with Class ClassTrust (fingerprint
// or hostname mismatch, or any other TLS failure) or ClassTemporary (the
// server could not be reached, or ctx ended first).
func DialTOFU(ctx context.Context, address, fingerprint string) (*grpc.ClientConn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &Error{Class: ClassAgentError, msg: "invalid server address " + address}
	}
	tlsConn, err := dialAndVerify(ctx, address, host, fingerprint)
	if err != nil {
		return nil, err
	}
	return newGRPCConn(address, tlsConn)
}

// dialAndVerify makes the TCP connection and runs the TLS handshake with
// the TOFU verifier; the handshake itself, via VerifyPeerCertificate,
// completes the fingerprint and hostname checks before any RPC exists.
func dialAndVerify(ctx context.Context, address, host, fingerprint string) (*tls.Conn, error) {
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, classifyDialError(ctx, err, address)
	}
	v := &tofuVerifier{fingerprint: fingerprint, host: host}
	tlsConn := tls.Client(rawConn, &tls.Config{
		MinVersion:            tls.VersionTLS12,
		InsecureSkipVerify:    true, // v.verify does the real verification
		VerifyPeerCertificate: v.verify,
		ServerName:            host,
		NextProtos:            []string{"h2"},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		if out := v.outcome(); out != nil {
			return nil, out
		}
		return nil, classifyHandshakeError(ctx, err, address)
	}
	return tlsConn, nil
}

// newGRPCConn hands the already-verified tlsConn to grpc as-is.
// grpc.NewClient never dials by itself; an established-but-unused tlsConn
// would sit open on the server until something closes it, so Connect is
// called right away.
func newGRPCConn(address string, tlsConn *tls.Conn) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(address,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return tlsConn, nil }),
		grpc.WithTransportCredentials(tofuCredentials{}),
	)
	if err != nil {
		_ = tlsConn.Close()
		return nil, &Error{Class: ClassAgentError, msg: "building the client connection failed", err: err}
	}
	conn.Connect()
	return conn, nil
}

func classifyDialError(ctx context.Context, err error, address string) error {
	if ctx.Err() != nil {
		return temporaryError(address, "timed out connecting to the server; the token is intact and the command can be retried")
	}
	return temporaryError(address, "could not reach the server; the token is intact and the command can be retried: %v", err)
}

// classifyHandshakeError: the TCP connection succeeded but the TLS
// handshake failed for a reason other than our own certificate check
// (v.outcome() is checked by the caller first) — a server that does not
// speak TLS at all, or drops the connection mid-handshake. That is a
// trust-class failure: the agent did reach *something* at the address, it
// just cannot be the Sard server the token names.
func classifyHandshakeError(ctx context.Context, err error, address string) error {
	if ctx.Err() != nil {
		return temporaryError(address, "timed out connecting to the server; the token is intact and the command can be retried")
	}
	return trustError("TLS handshake with the server failed: %v", err)
}

// tofuVerifier is tls.Config.VerifyPeerCertificate for DialTOFU. It keeps
// the first failure it finds so DialTOFU can turn it into a typed *Error;
// TLS may invoke the callback more than once on some retries, so the first
// verdict wins.
type tofuVerifier struct {
	fingerprint string
	host        string

	mu  sync.Mutex
	err *Error
}

func (v *tofuVerifier) verify(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if out := v.outcome(); out != nil {
		return out
	}
	certs, err := parseCerts(rawCerts)
	if err != nil {
		return v.fail(trustError("server presented a certificate that could not be parsed: %v", err))
	}
	if len(certs) == 0 {
		return v.fail(trustError("server presented no certificate"))
	}
	if e := v.checkChain(certs[0], certs[1:]); e != nil {
		return v.fail(e)
	}
	return nil
}

// checkChain runs the fingerprint check before the hostname check (В17:
// the fingerprint matters more than the name).
func (v *tofuVerifier) checkChain(leaf *x509.Certificate, candidateRoots []*x509.Certificate) *Error {
	root := matchingRoot(candidateRoots, v.fingerprint)
	if root == nil {
		return trustError("server CA fingerprint does not match the enrollment token: the token may be for another server, or this connection may be intercepted")
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return trustError("server certificate does not chain to the pinned CA: %v", err)
	}
	if err := leaf.VerifyHostname(v.host); err != nil {
		e := trustError("host %q is not covered by the server certificate; the address must match SARD_AGENT_ENDPOINT or one of the server certificate names", v.host)
		e.Names = certNames(leaf)
		return e
	}
	return nil
}

func (v *tofuVerifier) fail(e *Error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err == nil {
		v.err = e
	}
	return v.err
}

func (v *tofuVerifier) outcome() *Error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

func parseCerts(rawCerts [][]byte) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, nil
}

func matchingRoot(candidates []*x509.Certificate, fingerprint string) *x509.Certificate {
	for _, c := range candidates {
		if spkiFingerprint(c) == fingerprint {
			return c
		}
	}
	return nil
}

// spkiFingerprint is SHA-256 of the DER SubjectPublicKeyInfo, lowercase hex
// (docs/adr/0014-local-ca.md, "Отпечаток").
func spkiFingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

func certNames(c *x509.Certificate) []string {
	names := make([]string, 0, len(c.DNSNames)+len(c.IPAddresses))
	names = append(names, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	return names
}

// tofuCredentials hands the already-verified *tls.Conn from DialTOFU to
// grpc as-is: the handshake, including the fingerprint and hostname
// checks, already happened.
type tofuCredentials struct{}

func (tofuCredentials) ClientHandshake(_ context.Context, _ string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return nil, nil, errors.New("enroll: internal: expected an established TLS connection")
	}
	return tlsConn, credentials.TLSInfo{State: tlsConn.ConnectionState()}, nil
}

func (tofuCredentials) ServerHandshake(net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("enroll: server-side handshake is not supported")
}

func (tofuCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "tls"}
}

func (tofuCredentials) Clone() credentials.TransportCredentials { return tofuCredentials{} }

func (tofuCredentials) OverrideServerName(string) error { return nil }
