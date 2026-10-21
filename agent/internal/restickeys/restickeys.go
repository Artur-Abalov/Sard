// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restickeys

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Purpose is what a key is for.
type Purpose string

// The purposes of --for.
const (
	Verifier Purpose = "verifier"
	Recovery Purpose = "recovery"
	Other    Purpose = "other"
)

// userPrefix starts the user name of every key Sard issues (K1).
const userPrefix = "sard:"

// Tag is the purpose and the label Sard keeps in the user name of a key.
type Tag struct {
	Purpose Purpose
	Label   string
}

// User is the user name the key is created with: "sard:<purpose>" or
// "sard:<purpose>:<label>".
func (t Tag) User() string {
	if t.Label == "" {
		return userPrefix + string(t.Purpose)
	}
	return userPrefix + string(t.Purpose) + ":" + t.Label
}

// TagOf reads the tag from a user name; false for a key that Sard did not
// issue (made before V3, or by restic by hand).
func TagOf(user string) (Tag, bool) {
	rest, ok := strings.CutPrefix(user, userPrefix)
	if !ok {
		return Tag{}, false
	}
	purpose, label, _ := strings.Cut(rest, ":")
	if !validPurpose(Purpose(purpose)) {
		return Tag{}, false
	}
	return Tag{Purpose: Purpose(purpose), Label: label}, true
}

func validPurpose(p Purpose) bool { return slices.Contains([]Purpose{Verifier, Recovery, Other}, p) }

// ParsePurpose checks --for: given says whether the flag was on the command line.
func ParsePurpose(given bool, value string) (Purpose, *refusal.Failure) {
	switch {
	case !given:
		return "", refusal.Fail(refusal.KeyPurposeMissing, "say what the key is for: --for verifier, --for recovery or --for other --label <label>")
	case !validPurpose(Purpose(value)):
		return "", refusal.Fail(refusal.KeyPurposeInvalid, "--for %q is not a purpose: use --for verifier, --for recovery or --for other", value)
	}
	return Purpose(value), nil
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// CheckLabel checks --label: optional for a verifier and a recovery key,
// required for another purpose (K5).
func CheckLabel(p Purpose, given bool, label string) *refusal.Failure {
	switch {
	case !given && p == Other:
		return refusal.Fail(refusal.KeyLabelRequired, "--for other needs --label <label> to say whose key it is")
	case !given:
		return nil
	case !labelPattern.MatchString(label):
		return refusal.Fail(refusal.LabelInvalid, "--label %q must be 1 to 64 characters of A-Z a-z 0-9 - _ and start with a letter or a digit", label)
	}
	return nil
}

// Find is the key with the same purpose and the same label (no label is a
// value too), whoever issued it (K10).
func Find(keys []restic.Key, tag Tag) (restic.Key, bool) {
	for _, k := range keys {
		if got, ok := TagOf(k.UserName); ok && got == tag {
			return k, true
		}
	}
	return restic.Key{}, false
}

// HasRecovery says whether a key was issued for recovery, with any label.
func HasRecovery(keys []restic.Key) bool {
	return slices.ContainsFunc(keys, func(k restic.Key) bool {
		tag, ok := TagOf(k.UserName)
		return ok && tag.Purpose == Recovery
	})
}

// The codes of the warnings of a list of keys (K13).
const (
	WarnSingleKey     = "single_key"
	WarnNoRecoveryKey = "no_recovery_key"
)

// Warnings are the warnings a list of keys earns, in the order of K13.
func Warnings(keys []restic.Key) []string {
	var warnings []string
	if len(keys) == 1 {
		warnings = append(warnings, WarnSingleKey)
	}
	if !HasRecovery(keys) {
		warnings = append(warnings, WarnNoRecoveryKey)
	}
	return warnings
}

// Sorted is the keys in the order they were issued.
func Sorted(keys []restic.Key) []restic.Key {
	sorted := slices.Clone(keys)
	slices.SortStableFunc(sorted, func(a, b restic.Key) int { return a.Created.Compare(b.Created) })
	return sorted
}

var idPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8,64}$`)

// CheckID checks the id of key remove: 8 to 64 hexadecimal characters (K14).
func CheckID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("the key id %q must be 8 to 64 hexadecimal characters, as `sard-agent repo key list` prints it", id)
	}
	return nil
}

// Match are the keys whose id starts with the prefix.
func Match(keys []restic.Key, prefix string) []restic.Key {
	prefix = strings.ToLower(prefix)
	return slices.DeleteFunc(slices.Clone(keys), func(k restic.Key) bool { return !strings.HasPrefix(k.ID, prefix) })
}

// NewPassword is the password of a new key: 32 bytes of random in base64url
// without padding (K6), made as the password of a generated repository.
func NewPassword(random io.Reader) (string, error) { return repoinit.NewPassword(random) }
