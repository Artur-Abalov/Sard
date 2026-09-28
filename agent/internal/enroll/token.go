// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"encoding/base64"
	"errors"
	"strings"
)

// tokenPrefix is the fixed prefix of an enrollment token string.
const tokenPrefix = "sard_"

// secretLen is the encoded length of the token secret: 32 raw bytes,
// base64url without padding.
const secretLen = 43

// fingerprintLen is the length of the lowercase hex CA fingerprint.
const fingerprintLen = 64

// Token is a parsed enrollment token string (docs/specs/enrollment-token.md).
type Token struct {
	// Secret is the 32-byte CSPRNG secret.
	Secret []byte
	// Fingerprint is the SHA-256 of the pinned CA's SPKI, lowercase hex.
	Fingerprint string
}

// TokenError is returned by ParseToken. Its text never contains the token
// string; Reason is always "TOKEN_MALFORMED" (agent-side parsing does not
// distinguish the server's finer-grained reasons).
type TokenError struct {
	Reason string
	// detail is for logs only; it never repeats the input string.
	detail string
}

func (e *TokenError) Error() string { return "enrollment token: " + e.Reason + ": " + e.detail }

func malformed(detail string) *TokenError {
	return &TokenError{Reason: "TOKEN_MALFORMED", detail: detail}
}

// ParseToken parses s strictly per docs/specs/enrollment-token.md. Any
// violation returns a *TokenError; the token string never appears in the
// returned error.
func ParseToken(s string) (Token, error) {
	rest, ok := strings.CutPrefix(s, tokenPrefix)
	if !ok {
		return Token{}, malformed("missing sard_ prefix")
	}
	secretPart, fingerprint, err := splitOnce(rest)
	if err != nil {
		return Token{}, err
	}
	secret, err := decodeSecret(secretPart)
	if err != nil {
		return Token{}, err
	}
	if err := checkFingerprint(fingerprint); err != nil {
		return Token{}, err
	}
	return Token{Secret: secret, Fingerprint: fingerprint}, nil
}

func splitOnce(s string) (before, after string, err error) {
	if strings.Count(s, ".") != 1 {
		return "", "", malformed("want exactly one '.' separator")
	}
	before, after, _ = strings.Cut(s, ".")
	return before, after, nil
}

func decodeSecret(s string) ([]byte, error) {
	if len(s) != secretLen {
		return nil, malformed("secret: want 43 characters")
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, malformed("secret: not canonical base64url")
	}
	if len(secret) != 32 {
		return nil, malformed("secret: want 32 bytes")
	}
	return secret, nil
}

var errHexAlphabet = errors.New("not lowercase hex")

func checkFingerprint(s string) error {
	if len(s) != fingerprintLen {
		return malformed("fingerprint: want 64 characters")
	}
	for _, r := range s {
		if !isLowerHex(r) {
			return malformed("fingerprint: " + errHexAlphabet.Error())
		}
	}
	return nil
}

func isLowerHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

// NormalizeTokenFile drops exactly one trailing LF or CRLF from token file
// contents, as docs/specs/agent/agent-enroll.feature (В11) requires. Any
// other whitespace is left untouched, so ParseToken rejects it as
// TOKEN_MALFORMED and an empty file is left for the caller to reject as a
// usage error.
func NormalizeTokenFile(data []byte) string {
	s := string(data)
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2]
	}
	return strings.TrimSuffix(s, "\n")
}
