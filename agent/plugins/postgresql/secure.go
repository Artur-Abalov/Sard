// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The rules of a connection that carries a password (S11, ADR
// 00XX-draft-secret-binding): the operator of the agent host binds the secret
// to its targets, the plugin sends the password only over SCRAM, and to a
// server on the network only over TLS that checks the server's certificate,
// unless the binding allows less.
const (
	tlsVerifyFull = "verify-full"
	authSCRAM     = "scram-sha-256"
	// systemCA is the CA store of the operating system (libpq 16 and later).
	systemCA = "system"
)

// tlsOrder are the TLS modes from the weakest.
var tlsOrder = []string{tlsDisable, tlsRequire, tlsVerifyFull}

// secretHost gives a secret for a target: sdk.Host and sdk.RecoveryHost.
type secretHost interface {
	SecretFor(name string, target sdk.Target) (sdk.Secret, error)
}

// secured reads the password of the target for the database dbname, once the
// binding of its secret allows this plugin there, and judges the connection the
// binding allows. Nothing is sent to the server before.
func (t target) secured(h secretHost, dbname string) (target, []byte, error) {
	s, err := h.SecretFor(t.PasswordRef, sdk.Target{Host: t.Host, Port: t.Port, Database: dbname})
	if err != nil {
		return t, nil, err
	}
	t.grant = s.Grant
	if err := t.checkTLS(); err != nil {
		return t, nil, err
	}
	password, err := checkPassword(t.PasswordRef, s.Value)
	return t, password, err
}

// securedFor is secured for every database the plugin signs in to on the
// target: the password goes to none of them unless the binding allows each.
// An empty name is no database (a rollback of roles alone) and is not asked.
func (t target) securedFor(h secretHost, dbnames ...string) (target, []byte, error) {
	var password []byte
	var err error
	for _, db := range slices.DeleteFunc(dbnames, func(db string) bool { return db == "" }) {
		if t, password, err = t.secured(h, db); err != nil {
			return t, nil, err
		}
	}
	return t, password, nil
}

// socket: the host is a directory of Unix sockets.
func (t target) socket() bool { return strings.HasPrefix(t.Host, "/") }

// loopback: the server is on this host, over TCP.
func (t target) loopback() bool {
	return strings.EqualFold(t.Host, "localhost") || net.ParseIP(t.Host).IsLoopback()
}

// network: the password crosses a network, where TLS is required.
func (t target) network() bool { return !t.socket() && !t.loopback() }

// checkTLS refuses a TLS mode to a server on the network weaker than the
// binding allows: verify-full unless it says otherwise.
func (t target) checkTLS() error {
	allowed := firstNonEmpty(t.grant.TLS, tlsVerifyFull)
	mode := t.sslmode()
	if !t.network() || slices.Index(tlsOrder, mode) >= slices.Index(tlsOrder, allowed) {
		return nil
	}
	return &tlsRefusal{fmt.Sprintf("tls_mode %s would send the password of secret %q to %s:%d %s; the binding of the secret on the agent host allows %s at the weakest. "+
		"Use tls_mode verify-full, or allow it on the agent host: sudo sard-agent secret set %s --plugin postgresql --target ... --allow-tls %s",
		mode, t.PasswordRef, t.Host, t.Port, tlsWords[mode], allowed, t.PasswordRef, mode)}
}

// tlsRefusal is a TLS mode the binding does not allow: the secret is not
// allowed to go that way.
type tlsRefusal struct{ msg string }

func (e *tlsRefusal) Error() string { return e.msg }

// Is matches sdk.ErrSecretNotAllowed.
func (e *tlsRefusal) Is(target error) bool { return target == sdk.ErrSecretNotAllowed }

// secretReason is the reason of a restore whose secret cannot be used.
func secretReason(err error) string {
	if errors.Is(err, sdk.ErrSecretNotAllowed) {
		return ReasonSecretNotAllowed
	}
	return ReasonSecretInvalid
}

// tlsWords say what a mode leaves open.
var tlsWords = map[string]string{
	tlsDisable: "in clear text",
	tlsRequire: "over TLS without checking the server's certificate, to whoever answers at that address",
}

// sslmode is the TLS mode of the connection: verify-full for a server on the
// network, require for one on this host and disable for a socket, unless the
// config says otherwise.
func (t target) sslmode() string {
	switch {
	case t.TLSMode != "":
		return t.TLSMode
	case t.socket():
		return tlsDisable
	case t.loopback():
		return tlsRequire
	}
	return tlsVerifyFull
}

// rootCert is the CA the certificate of the server is checked with: the one
// the operator of the host bound, the one of the config, or the CA store of the
// system.
func (t target) rootCert() string {
	return firstNonEmpty(t.grant.CAFile, firstNonEmpty(t.TLSRootCert, systemCA))
}

// auth are the methods of authentication the server may ask for: SCRAM over
// TCP, anything on a socket, unless the binding says otherwise.
func (t target) auth() []string {
	switch {
	case len(t.grant.Auth) > 0:
		return t.grant.Auth
	case t.socket():
		return nil
	}
	return []string{authSCRAM}
}

// channelBinding: SCRAM over TLS binds the exchange to the server's
// certificate, so that a relay is caught, unless the binding allows without.
func (t target) channelBinding() bool {
	return t.sslmode() != tlsDisable && slices.Equal(t.auth(), []string{authSCRAM}) && !t.grant.NoChannelBinding
}

// securityPairs are the parameters of the connection string that protect the
// password.
func (t target) securityPairs() [][2]string {
	pairs := [][2]string{{"sslmode", t.sslmode()}}
	if t.sslmode() != tlsDisable {
		// libpq tries GSS encryption first, and a connection it encrypts has no
		// TLS: neither the certificate nor channel binding would be checked.
		pairs = append(pairs, [2]string{"gssencmode", "disable"})
	}
	if t.sslmode() == tlsVerifyFull {
		pairs = append(pairs, [2]string{"sslrootcert", t.rootCert()})
	}
	if auth := t.auth(); len(auth) > 0 {
		pairs = append(pairs, [2]string{"require_auth", strings.Join(auth, ",")})
	}
	if t.channelBinding() {
		pairs = append(pairs, [2]string{"channel_binding", "require"})
	}
	return pairs
}

// authHints say what to do about a refusal of libpq that keeps the password (S11),
// by the words of its message.
var authHints = []struct{ words, hint string }{
	{`invalid connection option "require_auth"`, clientTooOld},
	{`invalid connection option "channel_binding"`, clientTooOld},
	{"authentication method requirement", "the server asked for an authentication the binding of the secret does not allow, and the password was not sent. " +
		"Make the role sign in with SCRAM (password_encryption = 'scram-sha-256', then set its password again), " +
		"or allow the method on the agent host: sudo sard-agent secret set <secret> --plugin postgresql --target ... --auth scram-sha-256,md5"},
	{"channel binding", "the server does not offer SCRAM with channel binding over this connection (a pooler in between, or a role without a SCRAM password), and the password was not sent. " +
		"Fix the server, or allow it on the agent host: sudo sard-agent secret set <secret> --plugin postgresql --target ... --allow-no-channel-binding"},
	{"root certificate file", "the CA the server's certificate is checked with is not on the agent host: " +
		"bind the secret with it (sudo sard-agent secret set <secret> --plugin postgresql --target ... --ca-file <file>) or set tls_root_cert"},
}

const clientTooOld = "the PostgreSQL client on the agent host is older than 16 and cannot refuse a weak authentication, so the password was not sent: " +
	"install the PostgreSQL client 16 or newer, or point pg_dump_path to one (the agent image -pg18 has one)"

// hintFor is " — <what to do>" for a message of libpq that has a hint, "".
func hintFor(reason string) string {
	for _, h := range authHints {
		if strings.Contains(reason, h.words) {
			return " — " + h.hint
		}
	}
	return ""
}
