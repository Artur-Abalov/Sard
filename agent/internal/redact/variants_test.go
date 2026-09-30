// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"slices"
	"testing"
)

func TestVariantsCoverRawURLAndJSONForms(t *testing.T) {
	got := variants([]byte(`p@ss w/rd"<`))
	want := []string{
		`p@ss w/rd"<`,                // raw
		`p@ss w/rd\"` + uEsc("003c"), // JSON, HTML-safe (encoding/json default)
		`p@ss w/rd\"<`,               // JSON, SetEscapeHTML(false)
		`p%40ss+w%2Frd%22%3C`,        // url.QueryEscape
		`p%40ss+w%2frd%22%3c`,        // ... lowercase hex
		`p@ss%20w%2Frd%22%3C`,        // url.PathEscape
		`p@ss%20w%2frd%22%3c`,        // ... lowercase hex
		`p%40ss%20w%2Frd%22%3C`,      // password in URL userinfo
		`p%40ss%20w%2frd%22%3c`,      // ... lowercase hex
	}
	assertSameSet(t, got, want)
}

func TestVariantsDeduplicatePlainValue(t *testing.T) {
	assertSameSet(t, variants([]byte("hunter2hunter2")), []string{"hunter2hunter2"})
}

func TestVariantsJSONEscapeControlAndURLForms(t *testing.T) {
	// A newline is JSON-escaped in the raw form; an "&" survives
	// url.PathEscape and userinfo escaping, and the HTML-safe JSON encoder escapes it there too.
	got := variants([]byte("a&\nb"))
	want := []string{
		"a&\nb",
		"a" + uEsc("0026") + `\nb`, `a&\nb`,
		"a%26%0Ab", "a%26%0ab", // QueryEscape
		"a&%0Ab", "a&%0ab", // PathEscape and userinfo agree
		"a" + uEsc("0026") + "%0Ab", "a" + uEsc("0026") + "%0ab",
	}
	assertSameSet(t, got, want)
}

func TestLowerHexTouchesOnlyPercentEscapes(t *testing.T) {
	for in, want := range map[string]string{
		"AB%2F%C3%A9CD%": "AB%2f%c3%a9CD%",
		"%2F":            "%2f",
		"x%2F%A":         "x%2f%A", // an incomplete escape at the end is left alone
		"%GZ%AF":         "%GZ%af", // only A-F are hex digits
	} {
		if got := string(lowerHex([]byte(in))); got != want {
			t.Errorf("lowerHex(%q) = %q, want %q", in, got, want)
		}
	}
}

// uEsc builds a JSON \uXXXX escape.
func uEsc(hex string) string { return string(rune(92)) + "u" + hex }

func assertSameSet(t *testing.T, got [][]byte, want []string) {
	t.Helper()
	g := make([]string, len(got))
	for i, v := range got {
		g[i] = string(v)
	}
	slices.Sort(g)
	w := slices.Clone(want)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Fatalf("variants:\n got %q\nwant %q", g, w)
	}
}
