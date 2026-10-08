// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import "fmt"

// Class groups the typed outcomes of enrollment by the exit-code class A2b
// maps them to (docs/specs/agent/agent-enroll.feature, "Классы кодов выхода").
// A2a never assigns an exit code itself; it only classifies.
type Class string

const (
	// ClassAgentError: CSR_INVALID, HOSTNAME_INVALID, an unforeseen gRPC
	// code or response shape. Not the agent operator's fault to fix by
	// retrying; needs an updated agent.
	ClassAgentError Class = "agent-error"
	// ClassUsage: the server itself reports TOKEN_MALFORMED. A2a's own
	// ParseToken rejects a malformed token before it is ever sent, so this
	// only fires defensively if the server disagrees with that check.
	ClassUsage Class = "usage"
	// ClassTokenRefused: TOKEN_UNKNOWN, TOKEN_USED, TOKEN_EXPIRED, TOKEN_REVOKED.
	ClassTokenRefused Class = "token-refused"
	// ClassTrust: CA fingerprint mismatch, hostname mismatch, any other TLS
	// failure verifying the server, or TOKEN_FOREIGN_CA from the server.
	ClassTrust Class = "trust"
	// ClassTemporary: server unreachable, timeout, INTERNAL_RETRYABLE,
	// interrupted after the request was sent.
	ClassTemporary Class = "temporary"
	// ClassWrite: a target directory is not writable, or writing the
	// identity files failed.
	ClassWrite Class = "write"
)

// Error is every typed outcome A2a produces past local, purely syntactic
// checks. A2b switches on Class to pick an exit code and on Reason to word
// its message; both are stable, machine-checkable values.
type Error struct {
	Class Class
	// Reason is the wire reason string from the server's ErrorInfo
	// (TOKEN_USED, CSR_INVALID, …), or "" when the error has no server
	// counterpart (a local TLS or network failure).
	Reason string
	// Address is set for unreachable/timeout errors (ClassTemporary).
	Address string
	// Names are the server certificate's subject names, set for a hostname
	// mismatch (В17: the message lists them).
	Names []string
	// TokenMaybeSpent is set when the failure happened after the Enroll
	// request was sent, so a retry with the same token may be refused
	// TOKEN_USED (В14).
	TokenMaybeSpent bool
	// Code is the gRPC code of an unforeseen response, set for
	// ClassAgentError outcomes with no Reason.
	Code string

	msg string
	err error
}

func (e *Error) Error() string {
	s := string(e.Class)
	if e.Reason != "" {
		s += ": " + e.Reason
	}
	if e.msg != "" {
		s += ": " + e.msg
	}
	if e.err != nil {
		s += ": " + e.err.Error()
	}
	return s
}

func (e *Error) Unwrap() error { return e.err }

func trustError(format string, a ...any) *Error {
	return &Error{Class: ClassTrust, msg: fmt.Sprintf(format, a...)}
}

func temporaryError(address, format string, a ...any) *Error {
	return &Error{Class: ClassTemporary, Address: address, msg: fmt.Sprintf(format, a...)}
}

// NewWriteError is a ClassWrite error for a local problem the command
// found itself, such as a missing directory.
func NewWriteError(msg string, err error) *Error {
	return &Error{Class: ClassWrite, msg: msg, err: err}
}
