// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// HostKey is a public key of a server: the type and the base64 blob as
// ssh-keyscan and known_hosts write them.
type HostKey struct {
	Type, Blob string
}

// Fingerprint is what ssh-keygen -l prints: SHA256 of the blob in base64
// without padding.
func (k HostKey) Fingerprint() string {
	raw, err := base64.StdEncoding.DecodeString(k.Blob)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ParseKeyscan reads the keys of the output of ssh-keyscan, "host type
// blob" per line; comments and lines that are no key are skipped.
func ParseKeyscan(out string) []HostKey {
	var keys []HostKey
	for l := range strings.Lines(out) {
		if key, ok := parseKeyLine(strings.Fields(l)); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// parseKeyLine reads "host type blob [comment]".
func parseKeyLine(fields []string) (HostKey, bool) {
	if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
		return HostKey{}, false
	}
	key := HostKey{Type: fields[1], Blob: fields[2]}
	return key, key.Fingerprint() != ""
}

// CheckFingerprint: --host-key-fingerprint is SHA256:<43 characters of
// base64 without "="> (Р40); MD5 and anything else is a usage error.
func CheckFingerprint(fingerprint string) *refusal.Failure {
	if b64, found := strings.CutPrefix(fingerprint, "SHA256:"); found && len(b64) == 43 {
		if raw, err := base64.RawStdEncoding.DecodeString(b64); err == nil && len(raw) == sha256.Size {
			return nil
		}
	}
	return &refusal.Failure{Class: refusal.ClassUsage, Detail: "--host-key-fingerprint must be a fingerprint in the form SHA256:<43 characters of base64 without =>, as ssh-keygen -l prints it"}
}

// Known are the entries of known_hosts that apply to one host.
type Known struct {
	// Lines are the numbers (from 1) of the lines of the entries.
	Lines []int
	keys  []HostKey
}

// Matches says whether one of the keys is the key of an entry.
func (k Known) Matches(keys []HostKey) bool {
	for _, have := range k.keys {
		for _, offered := range keys {
			if have.Blob == offered.Blob {
				return true
			}
		}
	}
	return false
}

// Key is the key of an entry that one of the keys offered matches.
func (k Known) Key(keys []HostKey) (HostKey, bool) {
	for _, offered := range keys {
		for _, have := range k.keys {
			if have.Blob == offered.Blob {
				return offered, true
			}
		}
	}
	return HostKey{}, false
}

// FindKnown finds the entries of known_hosts for name, as ssh writes it
// (host for port 22, [host]:port for another): plain and hashed ones,
// lists of names, wildcards and negations. Lines with a marker (@revoked,
// @cert-authority) are no host's key.
func FindKnown(content []byte, name string) Known {
	var found Known
	for n, l := range strings.Split(string(content), "\n") {
		if key, hostField, ok := entryOf(l); ok && appliesTo(hostField, name) {
			found.Lines = append(found.Lines, n+1)
			found.keys = append(found.keys, key)
		}
	}
	return found
}

// entryOf reads a line of known_hosts that holds a key: its host field and
// key. Comments, lines with a marker and lines that are no entry are none.
func entryOf(l string) (key HostKey, hostField string, ok bool) {
	fields := strings.Fields(l)
	if len(fields) < 3 || strings.HasPrefix(fields[0], "#") || strings.HasPrefix(fields[0], "@") {
		return HostKey{}, "", false
	}
	key = HostKey{Type: fields[1], Blob: fields[2]}
	return key, fields[0], key.Fingerprint() != ""
}

// appliesTo: the host field of a line names the host.
func appliesTo(hostField, name string) bool {
	if strings.HasPrefix(hostField, "|") {
		return hashedMatches(hostField, name)
	}
	matched := false
	for pattern := range strings.SplitSeq(hostField, ",") {
		verdict := patternVerdict(pattern, name)
		if verdict < 0 {
			return false
		}
		matched = matched || verdict > 0
	}
	return matched
}

// patternVerdict: 1 if a pattern of the field names the host, -1 if a
// negated one does (the line is then not the host's at all), 0 if it
// says nothing of it.
func patternVerdict(pattern, name string) int {
	negated, isNegation := strings.CutPrefix(pattern, "!")
	switch {
	case isNegation && globMatches(negated, name):
		return -1
	case !isNegation && globMatches(pattern, name):
		return 1
	}
	return 0
}

// globMatches is the pattern matching of ssh: * is any run of characters,
// ? any one, the rest is itself, in either case. (path.Match would take
// the [ of [host]:port for a character class.)
func globMatches(pattern, name string) bool {
	return wildcard([]rune(strings.ToLower(pattern)), []rune(strings.ToLower(name)))
}

func wildcard(pattern, name []rune) bool {
	for len(pattern) > 0 {
		if pattern[0] == '*' {
			return starMatches(pattern[1:], name)
		}
		if len(name) == 0 || (pattern[0] != '?' && pattern[0] != name[0]) {
			return false
		}
		pattern, name = pattern[1:], name[1:]
	}
	return len(name) == 0
}

// starMatches: the pattern after a * matches some end of the name.
func starMatches(rest, name []rune) bool {
	for i := 0; i <= len(name); i++ {
		if wildcard(rest, name[i:]) {
			return true
		}
	}
	return false
}

// hashedMatches: |1|salt|hash is the HMAC-SHA1 of the name keyed by the salt.
func hashedMatches(hostField, name string) bool {
	parts := strings.Split(hostField, "|")
	if len(parts) != 4 || parts[1] != "1" {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(parts[2])
	want, err2 := base64.StdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(name))
	return hmac.Equal(mac.Sum(nil), want)
}

// AddHostKey appends the key of the host as one line; the rest of the file
// stays byte for byte.
func AddHostKey(content []byte, name string, key HostKey) []byte {
	out := bytes.Clone(content)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, name+" "+key.Type+" "+key.Blob+"\n"...)
}

// ReplaceHostKey drops the entries that name the host itself (plain or
// hashed; a list of names keeps the other names; a wildcard, which is
// others' too, stays) and appends the key.
func ReplaceHostKey(content []byte, name string, key HostKey) []byte {
	var kept strings.Builder
	for l := range strings.Lines(string(content)) {
		if rest, drop := dropName(l, name); !drop {
			kept.WriteString(l)
		} else if rest != "" {
			kept.WriteString(rest)
		}
	}
	return AddHostKey([]byte(kept.String()), name, key)
}

// dropName is the line without the host's name: rest is what stays of
// it ("" if nothing does); drop is false if the line has nothing to do
// with the host's own name (a wildcard that matches it is others' too).
func dropName(l, name string) (rest string, drop bool) {
	_, hostField, ok := entryOf(l)
	if !ok || !appliesTo(hostField, name) {
		return "", false
	}
	if strings.HasPrefix(hostField, "|") {
		return "", true
	}
	all := strings.Split(hostField, ",")
	names := slices.DeleteFunc(slices.Clone(all), func(pattern string) bool { return strings.EqualFold(pattern, name) })
	switch {
	case len(names) == len(all):
		return "", false
	case len(names) == 0:
		return "", true
	}
	return strings.Replace(l, hostField, strings.Join(names, ","), 1), true
}
