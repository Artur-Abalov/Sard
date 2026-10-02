// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import "encoding/base64"

// minBase64 is the shortest base64 text that is searched for: a shorter one
// would mask innocent text, and a value this short is warned about anyway.
const minBase64 = 4

// base64Forms returns the base64 texts a value can appear as (A7b): the
// standard and the URL alphabet, with and without "=" padding, and for each
// of the three alignments the value can have inside a longer base64 text
// (0, 1 or 2 bytes before it) the characters that depend on the value alone,
// plus the same run extended to the end of the text for a value that ends it.
func base64Forms(v []byte) [][]byte {
	var out [][]byte
	for _, shift := range []int{0, 1, 2} {
		from := (8*shift + 5) / 6 // the first character made of value bits only
		to := 8 * (shift + len(v)) / 6
		raw := append(make([]byte, shift), v...)
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			whole := []byte(enc.EncodeToString(raw))
			padded := []byte(enc.WithPadding(base64.StdPadding).EncodeToString(raw))
			for _, f := range [][]byte{whole[from:to], whole[from:], padded[from:]} {
				if len(f) >= minBase64 {
					out = append(out, f)
				}
			}
		}
	}
	return out
}
