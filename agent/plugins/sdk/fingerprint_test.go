// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func sample() sdk.Fingerprint {
	return sdk.Fingerprint{Plugin: "postgresql", Format: sdk.FingerprintFormat, Facts: []sdk.Fact{
		{Name: "tables", Rule: sdk.RuleExact, Value: "12"},
		{Name: "database_size", Rule: sdk.RuleInfo, Value: "8192"},
		{Name: "rows:public.a<b", Rule: sdk.RuleExact, Value: "3"},
	}}
}

// The canonical form sorts the keys and the facts and has no white space, so
// that the agent, the server and the verifier hash the same bytes.
func TestTheCanonicalFormSortsKeysAndFacts(t *testing.T) {
	got, err := sample().Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"facts":[{"name":"database_size","rule":"info","value":"8192"},{"name":"rows:public.a` + "\\u003c" + `b","rule":"exact","value":"3"},{"name":"tables","rule":"exact","value":"12"}],"format":1,"plugin":"postgresql"}`
	if string(got) != want {
		t.Errorf("Encode =\n%s\nwant\n%s", got, want)
	}
}

func TestTheOrderOfTheFactsDoesNotChangeTheHash(t *testing.T) {
	a := sample()
	b := sample()
	b.Facts[0], b.Facts[2] = b.Facts[2], b.Facts[0]
	ha, err := a.Hash()
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := b.Hash()
	if ha != hb {
		t.Errorf("hashes differ: %s, %s", ha, hb)
	}
	if a.Facts[0].Name != "tables" {
		t.Errorf("Encode reordered the facts of the caller: %v", a.Facts)
	}
}

// The hash is the lower-case hex SHA-256 of the canonical bytes, the value of
// the tag sard.fp and of VerifyTask.fingerprint_sha256.
func TestTheHashIsTheSHA256OfTheCanonicalBytes(t *testing.T) {
	b, _ := sample().Encode()
	sum := sha256.Sum256(b)
	got, err := sample().Hash()
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Errorf("Hash = %q, %v; want %x", got, err, sum)
	}
	if h := sdk.HashBytes(b); h != got {
		t.Errorf("HashBytes = %q, want %q", h, got)
	}
}

func TestDecodeReadsTheCanonicalForm(t *testing.T) {
	b, _ := sample().Encode()
	f, err := sdk.DecodeFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := f.Encode()
	if string(again) != string(b) {
		t.Errorf("round trip changed the bytes:\n%s\n%s", b, again)
	}
	if fact, ok := f.Lookup("tables"); !ok || fact.Value != "12" {
		t.Errorf("Lookup(tables) = %v, %v", fact, ok)
	}
	if _, ok := f.Lookup("absent"); ok {
		t.Error("Lookup found a fact that is not there")
	}
}

// Bytes that are not canonical would hash differently on the server: they are refused.
func TestDecodeRefusesBytesThatAreNotCanonical(t *testing.T) {
	for name, text := range map[string]string{
		"spaces":   `{ "facts":[],"format":1,"plugin":"files"}`,
		"order":    `{"format":1,"facts":[],"plugin":"files"}`,
		"unknown":  `{"facts":[],"format":1,"plugin":"files","x":1}`,
		"not json": `fingerprint`,
		"unsorted": `{"facts":[{"name":"b","rule":"exact","value":""},{"name":"a","rule":"exact","value":""}],"format":1,"plugin":"files"}`,
		"trailing": `{"facts":[],"format":1,"plugin":"files"}` + "\n",
	} {
		if _, err := sdk.DecodeFingerprint([]byte(text)); err == nil {
			t.Errorf("%s: DecodeFingerprint accepted %s", name, text)
		}
	}
}

func TestAnEmptyListOfFactsIsWrittenAsAnArray(t *testing.T) {
	b, err := sdk.Fingerprint{Plugin: "files", Format: 1}.Encode()
	if err != nil || string(b) != `{"facts":[],"format":1,"plugin":"files"}` {
		t.Errorf("Encode = %s, %v", b, err)
	}
}

func TestEncodeRefusesAnInvalidFingerprint(t *testing.T) {
	long := strings.Repeat("x", sdk.MaxFingerprintBytes)
	for name, f := range map[string]sdk.Fingerprint{
		"no plugin":     {Format: 1},
		"no format":     {Plugin: "files"},
		"newer format":  {Plugin: "files", Format: sdk.FingerprintFormat + 1},
		"empty name":    {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Rule: sdk.RuleExact}}},
		"repeated name": {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a", Rule: sdk.RuleExact}, {Name: "a", Rule: sdk.RuleInfo}}},
		"unknown rule":  {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a", Rule: "approx"}}},
		"bad utf-8":     {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a\xff", Rule: sdk.RuleExact}}},
		"bad value":     {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a", Rule: sdk.RuleExact, Value: "\xff"}}},
		"bad plugin":    {Plugin: "\xff", Format: 1},
		"too large":     {Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a", Rule: sdk.RuleExact, Value: long}}},
	} {
		_, err := f.Encode()
		if !errors.Is(err, sdk.ErrInvalidFingerprint) {
			t.Errorf("%s: Encode error = %v, want ErrInvalidFingerprint", name, err)
		}
		if _, err := f.Hash(); err == nil {
			t.Errorf("%s: Hash accepted it", name)
		}
	}
}

func TestDecodeRefusesAnInvalidFingerprint(t *testing.T) {
	_, err := sdk.DecodeFingerprint([]byte(`{"facts":[],"format":0,"plugin":"files"}`))
	if !errors.Is(err, sdk.ErrInvalidFingerprint) {
		t.Errorf("error = %v", err)
	}
}

func TestTheLargestFingerprintIsAccepted(t *testing.T) {
	f := sdk.Fingerprint{Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "a", Rule: sdk.RuleExact}}}
	b, _ := f.Encode()
	f.Facts[0].Value = strings.Repeat("x", sdk.MaxFingerprintBytes-len(b))
	if b, err := f.Encode(); err != nil || len(b) != sdk.MaxFingerprintBytes {
		t.Errorf("Encode of %d bytes: %v", len(b), err)
	}
}
