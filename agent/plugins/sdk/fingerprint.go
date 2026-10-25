// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// FingerprintFormat is the version of the format of Fingerprint this SDK
// writes and the newest it reads.
const FingerprintFormat = 1

// MaxFingerprintBytes bounds the canonical form of a fingerprint.
const MaxFingerprintBytes = 1 << 20

// ErrInvalidFingerprint is wrapped by every error about a fingerprint that
// breaks the rules of Fingerprint.
var ErrInvalidFingerprint = errors.New("invalid fingerprint")

// Rule says how the agent compares a fact of the source with the same fact of
// a restored copy.
type Rule string

// The rules of a fact.
const (
	// RuleExact: the copy must have the fact, with the same value.
	RuleExact Rule = "exact"
	// RuleInfo: shown, never compared (a size that a restore changes, a
	// count that could not be taken exactly).
	RuleInfo Rule = "info"
)

// Fact is one measured property of a source, such as the number of rows of a
// table or the SHA-256 of a file.
type Fact struct {
	// Name identifies the fact within the fingerprint, e.g. "rows:public.users".
	Name string `json:"name"`
	Rule Rule   `json:"rule"`
	// Value is the measure as text, e.g. "1042".
	Value string `json:"value"`
}

// Fingerprint is what a plugin measured of a source, or of a restored copy of
// it: the agent compares the two fact by fact, the plugin never does.
//
// Its canonical form (Encode) is JSON with the keys and the facts sorted and
// no white space; the hash of those bytes is the tag sard.fp of the snapshot.
// Names, values and the plugin are valid UTF-8, names are unique and not
// empty, and the canonical form is at most MaxFingerprintBytes.
type Fingerprint struct {
	// The fields are in the order of their keys: json writes them so.
	Facts  []Fact `json:"facts"`
	Format int    `json:"format"`
	Plugin string `json:"plugin"`
}

// Lookup returns the fact of that name.
func (f Fingerprint) Lookup(name string) (Fact, bool) {
	i := slices.IndexFunc(f.Facts, func(x Fact) bool { return x.Name == name })
	if i < 0 {
		return Fact{}, false
	}
	return f.Facts[i], true
}

// Encode returns the canonical form of the fingerprint.
func (f Fingerprint) Encode() ([]byte, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	sorted := Fingerprint{Facts: slices.Clone(f.Facts), Format: f.Format, Plugin: f.Plugin}
	if sorted.Facts == nil {
		sorted.Facts = []Fact{}
	}
	slices.SortFunc(sorted.Facts, func(a, b Fact) int { return cmp.Compare(a.Name, b.Name) })
	b, err := json.Marshal(sorted)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFingerprint, err)
	}
	if len(b) > MaxFingerprintBytes {
		return nil, fmt.Errorf("%w: %d bytes, more than %d", ErrInvalidFingerprint, len(b), MaxFingerprintBytes)
	}
	return b, nil
}

// Hash is the lower-case hex SHA-256 of the canonical form.
func (f Fingerprint) Hash() (string, error) {
	b, err := f.Encode()
	if err != nil {
		return "", err
	}
	return HashBytes(b), nil
}

// HashBytes is the lower-case hex SHA-256 of the canonical form b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DecodeFingerprint reads a fingerprint from its canonical form; other bytes,
// even of the same fingerprint, are refused, since their hash differs.
func DecodeFingerprint(b []byte) (Fingerprint, error) {
	var f Fingerprint
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return Fingerprint{}, fmt.Errorf("%w: %v", ErrInvalidFingerprint, err)
	}
	again, err := f.Encode()
	if err != nil {
		return Fingerprint{}, err
	}
	if !bytes.Equal(again, b) {
		return Fingerprint{}, fmt.Errorf("%w: not in the canonical form", ErrInvalidFingerprint)
	}
	return f, nil
}

func (f Fingerprint) check() error {
	switch {
	case f.Plugin == "" || !utf8.ValidString(f.Plugin):
		return fmt.Errorf("%w: the plugin is empty or not UTF-8", ErrInvalidFingerprint)
	case f.Format < 1 || f.Format > FingerprintFormat:
		return fmt.Errorf("%w: format %d, this agent reads 1 to %d", ErrInvalidFingerprint, f.Format, FingerprintFormat)
	}
	return checkFacts(f.Facts)
}

// checkFacts: each fact is valid and its name unique.
func checkFacts(facts []Fact) error {
	seen := make(map[string]bool, len(facts))
	for _, x := range facts {
		if err := x.check(); err != nil {
			return err
		}
		if seen[x.Name] {
			return fmt.Errorf("%w: fact %q twice", ErrInvalidFingerprint, x.Name)
		}
		seen[x.Name] = true
	}
	return nil
}

func (x Fact) check() error {
	switch {
	case x.Name == "" || !utf8.ValidString(x.Name):
		return fmt.Errorf("%w: a fact name is empty or not UTF-8", ErrInvalidFingerprint)
	case !utf8.ValidString(x.Value):
		return fmt.Errorf("%w: the value of %q is not UTF-8", ErrInvalidFingerprint, x.Name)
	case x.Rule != RuleExact && x.Rule != RuleInfo:
		return fmt.Errorf("%w: fact %q has rule %q", ErrInvalidFingerprint, x.Name, x.Rule)
	}
	return nil
}
