// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"encoding/base64"
	"strings"
	"testing"
)

// secretT has a length in bytes that is not a multiple of 3, and its standard
// and URL base64 differ ("é" and "?>>" make '+' and '/' appear).
const secretT = "p@ss w/rd? é>>~+&\"<token-24"

func b64Forms(t *testing.T, value string) map[string]string {
	t.Helper()
	forms := map[string]string{
		"std padded":   base64.StdEncoding.EncodeToString([]byte(value)),
		"std unpadded": base64.RawStdEncoding.EncodeToString([]byte(value)),
		"url padded":   base64.URLEncoding.EncodeToString([]byte(value)),
		"url unpadded": base64.RawURLEncoding.EncodeToString([]byte(value)),
	}
	if forms["std padded"] == forms["url padded"] || !strings.HasSuffix(forms["std padded"], "=") {
		t.Fatalf("the test value does not tell the base64 alphabets or the padding apart: %v", forms)
	}
	return forms
}

func TestBase64FormsOfAValueAreMasked(t *testing.T) {
	s := mustCompile(t, secretT)
	for name, form := range b64Forms(t, secretT) {
		if got := s.Mask("a " + form + " b"); got != "a [REDACTED] b" {
			t.Errorf("%s: Mask = %q", name, got)
		}
	}
}

func TestPaddingOfBase64IsAbsorbedByTheMarker(t *testing.T) {
	s := mustCompile(t, secretT)
	padded := b64Forms(t, secretT)["std padded"]
	if got := s.Mask("x=" + padded + ";"); got != "x=[REDACTED];" {
		t.Fatalf("Mask = %q", got)
	}
}

// determined are the characters of the base64 of prefix+value that depend
// on the value alone, however it is followed.
func determined(prefix, value string) string {
	enc := base64.RawStdEncoding.EncodeToString([]byte(prefix + value))
	from := (8*len(prefix) + 5) / 6
	to := 8 * (len(prefix) + len(value)) / 6
	return enc[from:to]
}

func TestAValueInsideALongerBase64TextIsMaskedAtEveryShift(t *testing.T) {
	s := mustCompile(t, secretT)
	for _, text := range []struct{ prefix, suffix string }{
		{"", ""}, {"u:", ""}, {"us:", ""}, {"user:", ""}, {"", "\n"}, {"user:", "\n"}, {"a", "bcd"},
	} {
		line := "Authorization Basic " + base64.StdEncoding.EncodeToString([]byte(text.prefix+secretT+text.suffix))
		got := s.Mask(line)
		if !strings.Contains(got, Marker) {
			t.Errorf("%q: no marker in %q", text, got)
		}
		d := determined(text.prefix, secretT)
		for i := 0; i+4 <= len(d); i++ {
			if strings.Contains(got, d[i:i+4]) {
				t.Errorf("%q: %q of the value is left in %q", text, d[i:i+4], got)
			}
		}
	}
}

func TestBase64ValueSplitAtEveryPlaceIsMasked(t *testing.T) {
	values := []string{secretT}
	for name, form := range b64Forms(t, secretT) {
		in := "a " + form + " b"
		for i := 1; i < len(in); i++ {
			if got := redactChunks(t, values, in[:i], in[i:]); got != "a [REDACTED] b" {
				t.Fatalf("%s split at %d: %q", name, i, got)
			}
		}
	}
}

func TestBase64TextThatIsNotTheValueIsLeftAlone(t *testing.T) {
	s := mustCompile(t, secretT)
	other := base64.StdEncoding.EncodeToString([]byte("some other long enough text, not a secret"))
	if got := s.Mask(other); got != other {
		t.Fatalf("Mask = %q", got)
	}
}

func TestShortBase64PatternsAreNotUsed(t *testing.T) {
	// "a" is "YQ==" in base64; "YQ" alone would mask innocent text.
	s := mustCompile(t, "a")
	if got := s.Mask("YQ YQ== a"); got != "YQ [REDACTED] [REDACTED]" {
		t.Fatalf("Mask = %q", got)
	}
}

func TestIsShortCountsCodePointsAndBytesOfInvalidUTF8(t *testing.T) {
	for value, want := range map[string]bool{
		"ab7": true, "abcd": false, "пар": true, "паро": false,
		"\xff\xfe\xfd": true, "\xff\xfe\xfd\xfc": false, "": true,
	} {
		if got := IsShort([]byte(value)); got != want {
			t.Errorf("IsShort(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestBase64OfAMultilineKeyOnOneLineIsOneMarker(t *testing.T) {
	s := mustCompile(t, pem)
	enc := base64.StdEncoding.EncodeToString([]byte(pem))
	if got := s.Mask("k " + enc + " e"); got != "k [REDACTED] e" {
		t.Fatalf("Mask = %q", got)
	}
}

func TestABase64TextShorterThanFourCharactersIsNotSearchedFor(t *testing.T) {
	s := mustCompile(t, "ab")
	for _, text := range []string{"xYWIx", "xhYgx"} { // "ab" unpadded, and after two bytes
		if got := s.Mask(text); got != text {
			t.Errorf("Mask(%q) = %q, the text is too short to be masked", text, got)
		}
	}
	if got := s.Mask("x YWI= x"); got != "x [REDACTED] x" {
		t.Errorf("padded form: Mask = %q", got)
	}
}
