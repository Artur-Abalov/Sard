// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
)

// variants returns the distinct byte strings a value can appear as in a
// tool's output: as is, URL-encoded (query, path segment, password in
// userinfo; upper- and lowercase hex), and each of those JSON-escaped with
// and without HTML escaping (restic writes --json output).
func variants(v []byte) [][]byte {
	s := string(v)
	encoded := []string{url.QueryEscape(s), url.PathEscape(s), userinfoPassword(s)}
	forms := []string{s}
	for _, e := range encoded {
		forms = append(forms, e, string(lowerHex([]byte(e))))
	}
	var out [][]byte
	seen := map[string]bool{}
	for _, f := range forms {
		for _, g := range []string{f, jsonEscape(f, true), jsonEscape(f, false)} {
			if !seen[g] {
				seen[g] = true
				out = append(out, []byte(g))
			}
		}
	}
	return out
}

func userinfoPassword(s string) string {
	return strings.TrimPrefix(url.UserPassword("u", s).String(), "u:")
}

// jsonEscape is s as encoding/json writes it inside a string literal.
func jsonEscape(s string, html bool) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(html)
	_ = enc.Encode(s) // encoding a string cannot fail
	out := buf.String()
	return out[1 : len(out)-2] // strip the quotes and Encode's newline
}

// lowerHex lowercases the hex digits of every %XX escape in v. In URL
// escaping output a "%" only ever starts an escape.
func lowerHex(v []byte) []byte {
	out := bytes.Clone(v)
	for i := 0; i+2 < len(out); i++ {
		if out[i] == '%' {
			out[i+1] = lowerByte(out[i+1])
			out[i+2] = lowerByte(out[i+2])
		}
	}
	return out
}

func lowerByte(b byte) byte {
	if 'A' <= b && b <= 'F' {
		return b + 'a' - 'A'
	}
	return b
}
