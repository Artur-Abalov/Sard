// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restickeys_test

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

func key(id, user string, created int) restic.Key {
	return restic.Key{ID: strings.Repeat(id, 64/len(id)), UserName: user, HostName: "src1",
		Created: time.Date(2026, 10, 20, 10, created, 0, 0, time.UTC)}
}

// K1: the purpose and the label live in the user name of the key.
func TestTheTagIsWrittenToTheUserNameOfTheKey(t *testing.T) {
	cases := []struct {
		tag  restickeys.Tag
		want string
	}{
		{restickeys.Tag{Purpose: restickeys.Recovery}, "sard:recovery"},
		{restickeys.Tag{Purpose: restickeys.Verifier, Label: "vrf-1"}, "sard:verifier:vrf-1"},
		{restickeys.Tag{Purpose: restickeys.Other, Label: "auditor"}, "sard:other:auditor"},
	}
	for _, c := range cases {
		if got := c.tag.User(); got != c.want {
			t.Errorf("User() = %q, want %q", got, c.want)
		}
		if back, ok := restickeys.TagOf(c.want); !ok || back != c.tag {
			t.Errorf("TagOf(%q) = %+v, %v", c.want, back, ok)
		}
	}
}

func TestAKeyNotMadeBySardHasNoTag(t *testing.T) {
	for _, user := range []string{"", "sard-agent", "root", "sard:", "sard:admin", "sard:admin:x", "Sard:recovery"} {
		if tag, ok := restickeys.TagOf(user); ok {
			t.Errorf("TagOf(%q) = %+v, want none", user, tag)
		}
	}
}

func TestParsePurposeAcceptsTheThreePurposes(t *testing.T) {
	for _, p := range []string{"verifier", "recovery", "other"} {
		if got, f := restickeys.ParsePurpose(true, p); f != nil || string(got) != p {
			t.Errorf("ParsePurpose(%q) = %q, %v", p, got, f)
		}
	}
}

func TestParsePurposeRefusesWhatIsMissingOrUnknown(t *testing.T) {
	if _, f := restickeys.ParsePurpose(false, ""); f == nil || f.Reason != refusal.KeyPurposeMissing ||
		!strings.Contains(f.Detail, "--for verifier, --for recovery or --for other") {
		t.Errorf("missing purpose: %v", f)
	}
	for _, value := range []string{"admin", ""} {
		if _, f := restickeys.ParsePurpose(true, value); f == nil || f.Reason != refusal.KeyPurposeInvalid {
			t.Errorf("purpose %q: %v", value, f)
		}
	}
}

func TestCheckLabel(t *testing.T) {
	cases := []struct {
		purpose restickeys.Purpose
		set     bool
		label   string
		want    refusal.Reason
	}{
		{restickeys.Verifier, false, "", ""},
		{restickeys.Recovery, false, "", ""},
		{restickeys.Other, false, "", refusal.KeyLabelRequired},
		{restickeys.Other, true, "", refusal.LabelInvalid},
		{restickeys.Verifier, true, "-vrf", refusal.LabelInvalid},
		{restickeys.Verifier, true, "_vrf", refusal.LabelInvalid},
		{restickeys.Verifier, true, "vrf.1", refusal.LabelInvalid},
		{restickeys.Verifier, true, "vrf 1", refusal.LabelInvalid},
		{restickeys.Verifier, true, "vrf:1", refusal.LabelInvalid},
		{restickeys.Verifier, true, strings.Repeat("a", 65), refusal.LabelInvalid},
		{restickeys.Other, true, strings.Repeat("a", 64), ""},
		{restickeys.Other, true, "9Ab_-", ""},
		{restickeys.Recovery, true, "safe-box", ""},
	}
	for _, c := range cases {
		f := restickeys.CheckLabel(c.purpose, c.set, c.label)
		switch {
		case c.want == "" && f != nil, c.want != "" && (f == nil || f.Reason != c.want):
			t.Errorf("CheckLabel(%s, %v, %q) = %v, want %q", c.purpose, c.set, c.label, f, c.want)
		}
	}
}

func TestFindSeesTheSamePurposeAndTheSameLabelWhoeverIssuedIt(t *testing.T) {
	keys := []restic.Key{key("a1", "sard-agent", 0), key("b2", "sard:verifier:vrf-1", 1), key("c3", "sard:recovery", 2)}
	cases := []struct {
		tag  restickeys.Tag
		want bool
	}{
		{restickeys.Tag{Purpose: restickeys.Verifier, Label: "vrf-1"}, true},
		{restickeys.Tag{Purpose: restickeys.Verifier, Label: "vrf-2"}, false},
		{restickeys.Tag{Purpose: restickeys.Verifier}, false},
		{restickeys.Tag{Purpose: restickeys.Recovery}, true},
		{restickeys.Tag{Purpose: restickeys.Recovery, Label: "box"}, false},
	}
	for _, c := range cases {
		if _, got := restickeys.Find(keys, c.tag); got != c.want {
			t.Errorf("Find(%+v) = %v, want %v", c.tag, got, c.want)
		}
	}
}

func TestHasRecoveryAcceptsAnyLabel(t *testing.T) {
	for user, want := range map[string]bool{"sard:recovery": true, "sard:recovery:box-2": true, "sard:verifier:vrf-1": false, "sard-agent": false, "": false} {
		if got := restickeys.HasRecovery([]restic.Key{key("a1", user, 0)}); got != want {
			t.Errorf("HasRecovery(%q) = %v, want %v", user, got, want)
		}
	}
	if restickeys.HasRecovery(nil) {
		t.Error("no keys have a recovery key")
	}
}

func TestWarnings(t *testing.T) {
	h, r, v := key("a1", "sard-agent", 0), key("b2", "sard:recovery", 1), key("c3", "sard:verifier:vrf-1", 2)
	cases := []struct {
		keys []restic.Key
		want []string
	}{
		{[]restic.Key{h}, []string{"single_key", "no_recovery_key"}},
		{[]restic.Key{h, v}, []string{"no_recovery_key"}},
		{[]restic.Key{h, r}, nil},
		{[]restic.Key{r}, []string{"single_key"}},
	}
	for _, c := range cases {
		if got := restickeys.Warnings(c.keys); !slices.Equal(got, c.want) {
			t.Errorf("Warnings(%d keys) = %v, want %v", len(c.keys), got, c.want)
		}
	}
}

func TestSortedPutsTheKeysInTheOrderTheyWereIssued(t *testing.T) {
	a, b, c := key("a1", "", 3), key("b2", "", 1), key("c3", "", 2)
	got := restickeys.Sorted([]restic.Key{a, b, c})
	if got[0].ID != b.ID || got[1].ID != c.ID || got[2].ID != a.ID {
		t.Errorf("order %v", got)
	}
}

func TestCheckID(t *testing.T) {
	for id, ok := range map[string]bool{
		"abc": false, "abcdef1": false, "abcdef12": true, "ABCDEF12": true, "xyz12345": false,
		strings.Repeat("a", 64): true, strings.Repeat("a", 65): false, "": false,
	} {
		if got := restickeys.CheckID(id); (got == nil) != ok {
			t.Errorf("CheckID(%q) = %v, want ok=%v", id, got, ok)
		}
	}
}

func TestMatchByPrefix(t *testing.T) {
	r := key("abcd1234", "sard:recovery", 0)
	v := restic.Key{ID: "abcd1234" + strings.Repeat("f", 56), UserName: "sard:verifier:vrf-1"}
	h := key("ee", "", 2)
	keys := []restic.Key{h, r, v}
	if got := restickeys.Match(keys, "abcd1234"); len(got) != 2 {
		t.Errorf("prefix of two keys matched %d", len(got))
	}
	if got := restickeys.Match(keys, strings.ToUpper(h.ID[:10])); len(got) != 1 || got[0].ID != h.ID {
		t.Errorf("upper-case prefix matched %v", got)
	}
	if got := restickeys.Match(keys, "deadbeef"); len(got) != 0 {
		t.Errorf("matched %v", got)
	}
}

// K6: the password of a key is 32 bytes of the generator in base64url without
// padding, as the password of a generated repository (В1 of repo-init).
func TestNewPasswordIs43CharactersOfBase64urlFromTheGenerator(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 0xff - byte(i)
	}
	got, err := restickeys.NewPassword(bytes.NewReader(raw))
	if err != nil || got != base64.RawURLEncoding.EncodeToString(raw) || len(got) != 43 {
		t.Fatalf("NewPassword = %q, %v", got, err)
	}
	if _, err := restickeys.NewPassword(bytes.NewReader(raw[:5])); err == nil {
		t.Error("a generator that gives too few bytes is no error")
	}
}
