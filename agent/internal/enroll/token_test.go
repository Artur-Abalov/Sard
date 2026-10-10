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

// Each malformed-input class must produce the specific detail text naming
// the violation actually found, not just any TOKEN_MALFORMED error — a
// weaker check (reason only) cannot tell "the '.' separator check ran"
// from "some later, unrelated check happened to fail on the same input".
func TestParseTokenErrorDetailNamesTheSpecificViolationFound(t *testing.T) {
	cases := map[string]struct {
		input      string
		wantDetail string
	}{
		"no dots at all": {
			input:      "sard_" + strings.Repeat("A", 43) + vectorFingerprint,
			wantDetail: "one '.' separator",
		},
		"two dots": {
			input:      vectorToken + ".extra",
			wantDetail: "one '.' separator",
		},
		"secret 42 chars": {
			input:      "sard_" + strings.Repeat("A", 42) + "." + vectorFingerprint,
			wantDetail: "want 43 characters",
		},
		"non base64url secret": {
			input:      "sard_" + strings.Repeat("+", 43) + "." + vectorFingerprint,
			wantDetail: "not canonical base64url",
		},
		// isLowerHex must reject a byte below '0' (ASCII 0x30) even though
		// it is <= '9': a naive range check that only tests the upper bound
		// would wrongly accept it.
		"fingerprint has a byte below '0'": {
			input:      "sard_" + strings.Repeat("A", 43) + "." + "/" + vectorFingerprint[1:],
			wantDetail: "not lowercase hex",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := enroll.ParseToken(c.input)
			if err == nil {
				t.Fatal("ParseToken: want an error")
			}
			if !strings.Contains(err.Error(), c.wantDetail) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), c.wantDetail)
			}
		})
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

func TestTokenDigestIsTheHexSha256OfTheToken(t *testing.T) {
	if got, want := enroll.TokenDigest("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Fatalf("TokenDigest = %q, want %q", got, want)
	}
}
