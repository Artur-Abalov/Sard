// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// The test vector from docs/specs/enrollment-token.md.
const (
	vectorToken       = "sard_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8.8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"
	vectorFingerprint = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"
)

func TestParseTokenReadsTheTestVector(t *testing.T) {
	tok, err := enroll.ParseToken(vectorToken)
	if err != nil {
		t.Fatalf("ParseToken(vector): %v", err)
	}
	if tok.Fingerprint != vectorFingerprint {
		t.Fatalf("fingerprint = %q, want %q", tok.Fingerprint, vectorFingerprint)
	}
	if len(tok.Secret) != 32 {
		t.Fatalf("secret length = %d, want 32 bytes", len(tok.Secret))
	}
	for i, b := range tok.Secret {
		if b != byte(i) {
			t.Fatalf("secret[%d] = %#x, want %#x", i, b, i)
		}
	}
}

func TestParseTokenRejectsMalformedStrings(t *testing.T) {
	cases := map[string]string{
		"no sard_ prefix":        strings.TrimPrefix(vectorToken, "sard_"),
		"two dots":               vectorToken + ".extra",
		"secret 42 chars":        "sard_" + strings.Repeat("A", 42) + "." + vectorFingerprint,
		"uppercase fingerprint":  "sard_" + strings.Repeat("A", 43) + "." + strings.ToUpper(vectorFingerprint),
		"fingerprint 63 chars":   "sard_" + strings.Repeat("A", 43) + "." + vectorFingerprint[1:],
		"longer than 113 chars":  vectorToken + "x",
		"shorter than 113 chars": vectorToken[:len(vectorToken)-1],
		"non base64url secret":   "sard_" + strings.Repeat("+", 43) + "." + vectorFingerprint,
		"non canonical secret":   "sard_" + strings.Repeat("A", 42) + "B" + "." + vectorFingerprint,
		"non hex fingerprint":    "sard_" + strings.Repeat("A", 43) + "." + strings.Repeat("g", 64),
		"empty string":           "",
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := enroll.ParseToken(s)
			if err == nil {
				t.Fatal("ParseToken: want an error")
			}
			var terr *enroll.TokenError
			if !errors.As(err, &terr) {
				t.Fatalf("error type = %T, want *enroll.TokenError", err)
			}
			if terr.Reason != "TOKEN_MALFORMED" {
				t.Fatalf("reason = %q, want TOKEN_MALFORMED", terr.Reason)
			}
			if strings.Contains(err.Error(), s) && s != "" {
				t.Fatalf("error text contains the token string: %q", err.Error())
			}
		})
	}
}

func TestParseTokenErrorNeverContainsTheToken(t *testing.T) {
	_, err := enroll.ParseToken(vectorToken[:len(vectorToken)-1])
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), vectorToken) || strings.Contains(err.Error(), vectorToken[:len(vectorToken)-1]) {
		t.Fatalf("error text leaks the token: %q", err.Error())
	}
}

func TestNormalizeTokenFileDropsOneTrailingNewline(t *testing.T) {
	cases := map[string][]byte{
		"LF":   []byte(vectorToken + "\n"),
		"CRLF": []byte(vectorToken + "\r\n"),
		"none": []byte(vectorToken),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			got := enroll.NormalizeTokenFile(data)
			if got != vectorToken {
				t.Fatalf("NormalizeTokenFile(%q) = %q, want %q", data, got, vectorToken)
			}
		})
	}
}

func TestNormalizeTokenFileKeepsOtherWhitespaceIntact(t *testing.T) {
	// Only exactly one trailing LF or CRLF is dropped; everything else
	// (leading space, a second trailing newline, a second line) is left for
	// ParseToken to reject as TOKEN_MALFORMED.
	data := []byte(vectorToken + "\n\n")
	got := enroll.NormalizeTokenFile(data)
	if got != vectorToken+"\n" {
		t.Fatalf("NormalizeTokenFile(%q) = %q, want %q", data, got, vectorToken+"\n")
	}
}
