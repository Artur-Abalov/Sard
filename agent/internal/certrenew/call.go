// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// CallError is how a Caller tells the renewal what the server said to a
// RenewCertificate or ConfirmCertificate call; the renewal knows nothing of
// the protocol beneath (transport.CertClient makes these from gRPC statuses).
// Any other error of a call is a failure of the connection or of the agent,
// and is retried.
type CallError struct {
	// Text says what happened on one line without control characters, e.g.
	// "Unavailable INTERNAL_RETRYABLE"; it goes to the state file and the log.
	Text string
	// Final: the server will say the same to the same request (П20).
	Final bool
	// Unsupported: the server does not know the call (a server without renewal).
	Unsupported bool
	// RetryAfter is the least pause the server asks for before the next try.
	RetryAfter time.Duration
	Err        error
}

func (e *CallError) Error() string { return e.Text }

func (e *CallError) Unwrap() error { return e.Err }

func callError(err error) *CallError {
	var ce *CallError
	if errors.As(err, &ce) {
		return ce
	}
	return nil
}

// describe is err on one line: the text of a server's answer, or the text of
// any other error with control characters written as Go writes them.
func describe(err error) string {
	if ce := callError(err); ce != nil {
		return ce.Text
	}
	var b strings.Builder
	for _, r := range err.Error() {
		if unicode.IsControl(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
