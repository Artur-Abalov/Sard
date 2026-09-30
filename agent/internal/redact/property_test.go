// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"bytes"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// A small alphabet makes overlaps, prefixes and near-misses frequent; it
// contains none of the marker's bytes, so the output splits on the marker
// unambiguously. "%" and "\n" exercise the URL and JSON variants.
const alphabet = "ab%\n"

// splitsPerCase random chunkings are checked against each generated text.
const splitsPerCase = 10

func textGen(maxLen int) *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune(alphabet)), 0, maxLen, -1)
}

// reference masks the whole text at once, naively: every byte covered by
// an occurrence of any variant is covered, and each run of covered bytes
// becomes one marker.
func reference(values []string, text string) string {
	covered := make([]bool, len(text))
	for _, v := range values {
		for _, p := range variants([]byte(v)) {
			cover(covered, text, string(p))
		}
	}
	var b strings.Builder
	for i := range len(text) {
		switch {
		case !covered[i]:
			b.WriteByte(text[i])
		case i == 0 || !covered[i-1]:
			b.WriteString(Marker)
		}
	}
	return b.String()
}

// cover marks every byte of every occurrence of p in text.
func cover(covered []bool, text, p string) {
	for i := 0; i+len(p) <= len(text); i++ {
		if strings.HasPrefix(text[i:], p) {
			for j := i; j < i+len(p); j++ {
				covered[j] = true
			}
		}
	}
}

// splitAt cuts text into chunks at the given (unsorted, possibly repeated)
// positions; empty chunks are kept on purpose.
func splitAt(text string, cuts []int) []string {
	chunks := make([]string, 0, len(cuts)+1)
	prev := 0
	for _, c := range cuts {
		c = max(prev, min(c, len(text)))
		chunks = append(chunks, text[prev:c])
		prev = c
	}
	return append(chunks, text[prev:])
}

func TestPropertyChunkingDoesNotMatter(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		values := rapid.SliceOfN(textGen(12).Filter(func(s string) bool { return s != "" }), 0, 6).Draw(rt, "values")
		// Insert values into random filler so matches actually occur.
		parts := rapid.SliceOfN(rapid.OneOf(textGen(8), rapid.SampledFrom(append([]string{""}, values...))), 0, 20).Draw(rt, "parts")
		text := strings.Join(parts, "")
		whole := redactChunks(t, values, text)
		if want := reference(values, text); whole != want {
			rt.Fatalf("one chunk:\n got %q\nwant %q", whole, want)
		}
		for range splitsPerCase {
			cuts := rapid.SliceOfN(rapid.IntRange(0, len(text)), 0, 12).Draw(rt, "cuts")
			if got := redactChunks(t, values, splitAt(text, cuts)...); got != whole {
				rt.Fatalf("chunks %q:\n got %q\nwant %q", splitAt(text, cuts), got, whole)
			}
		}
		for _, seg := range strings.Split(whole, Marker) {
			for _, v := range values {
				if strings.Contains(seg, v) {
					rt.Fatalf("value %q survived in %q", v, whole)
				}
			}
		}
	})
}

func TestPropertyTextWithoutSecretsPassesUnchanged(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Values use bytes the text never contains.
		values := rapid.SliceOfN(rapid.StringMatching(`[xyz]{1,10}`), 0, 5).Draw(rt, "values")
		text := rapid.SliceOfN(rapid.Byte().Filter(func(b byte) bool { return !bytes.ContainsRune([]byte("xyz"), rune(b)) }), 0, 300).Draw(rt, "text")
		cuts := rapid.SliceOfN(rapid.IntRange(0, len(text)), 0, 10).Draw(rt, "cuts")
		if got := redactChunks(t, values, splitAt(string(text), cuts)...); got != string(text) {
			rt.Fatalf("text changed:\n got %q\nwant %q", got, text)
		}
	})
}
