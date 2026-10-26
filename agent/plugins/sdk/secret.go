// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ErrSecretNotAllowed is matched by a *BindingError.
var ErrSecretNotAllowed = errors.New("secret not allowed")

// Target is where a plugin is about to send the value of a secret: the server
// it signs in to (ADR 0008, S11). The operator of the host binds a secret to a
// plugin and to the targets it may go to; the agent gives the value for a
// target of the binding only.
type Target struct {
	// Host is a host name, an IP address, or the absolute path of a directory
	// of Unix sockets.
	Host string
	// Port is the TCP port, or the number of the socket file; 0 when the
	// plugin has none.
	Port int
	// Database is the database, "" when the plugin has none.
	Database string
}

// String is the target as the operator binds it: host[:port][/database], an
// IPv6 address in brackets.
func (t Target) String() string {
	s := t.Host
	if t.Port != 0 {
		s = net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	}
	if t.Database != "" {
		s += "/" + t.Database
	}
	return s
}

// Grant is what the operator of the host allows a secret beyond the plugin's
// own rules: a plugin applies its strictest rule where a field is empty.
type Grant struct {
	// Auth are the authentication methods the server may ask for, as the
	// plugin names them; empty: the plugin's default.
	Auth []string
	// TLS is the weakest TLS mode allowed ("require", "disable"); empty: the
	// plugin's default.
	TLS string
	// CAFile is the CA certificate, on the host, the server's certificate is
	// checked with; empty: none set by the operator.
	CAFile string
	// NoChannelBinding allows a TLS connection without channel binding.
	NoChannelBinding bool
}

// Secret is the value of a secret and what its binding allows.
type Secret struct {
	// Value is the content of the file of the secret. It must never be logged.
	Value []byte
	Grant Grant
}

// BindingError is returned by Host.SecretFor for a secret the operator of
// the host did not allow for this plugin or this target. It never holds the
// value.
type BindingError struct {
	Name   string
	Plugin string
	Target Target
	// Unbound: the secret has no binding at all.
	Unbound bool
	// BoundTo is the plugin the secret is bound to, when it is another one.
	BoundTo string
}

func (e *BindingError) Error() string {
	fix := fmt.Sprintf("sudo sard-agent secret set %s --plugin %s --target %s", e.Name, e.Plugin, e.Target)
	var b strings.Builder
	switch {
	case e.Unbound:
		fmt.Fprintf(&b, "secret %q has no binding on this host and is not given to any plugin; run on the agent host: ", e.Name)
	case e.BoundTo != "":
		fmt.Fprintf(&b, "secret %q is bound to the plugin %s, not %s; run on the agent host: ", e.Name, e.BoundTo, e.Plugin)
	default:
		fmt.Fprintf(&b, "secret %q is not allowed for %s at %s; if that is intended, run on the agent host: ", e.Name, e.Plugin, e.Target)
	}
	return b.String() + fix
}

// Is matches ErrSecretNotAllowed.
func (e *BindingError) Is(target error) bool { return target == ErrSecretNotAllowed }
