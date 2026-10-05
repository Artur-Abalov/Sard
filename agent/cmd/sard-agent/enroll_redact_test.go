// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import "testing"

// OQ-026: a token is redacted by its alphabet, not up to the next space.

const (
	// The test vector of docs/specs/enrollment-token.md.
	redactSecret      = "sard_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
	redactFingerprint = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"
	redactedMark      = "<redacted: looks like a token>"
)

func TestRedactionKeepsTheCharacterAfterAToken(t *testing.T) {
	for _, after := range []string{":", `"`, ")", ","} {
		if got, want := redactIfToken("x"+redactSecret+"."+redactFingerprint+after+"y"), "x"+redactedMark+after+"y"; got != want {
			t.Errorf("after %q: got %q, want %q", after, got, want)
		}
	}
}

func TestAFullTokenIsRedactedCompletely(t *testing.T) {
	for name, token := range map[string]string{
		"with a fingerprint":    redactSecret + "." + redactFingerprint,
		"without a fingerprint": redactSecret,
	} {
		if got := redactIfToken("open " + token + ": no such file"); got != "open "+redactedMark+": no such file" {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

// A path that merely contains "sard_" is redacted too; the owner accepted it.
func TestAPathWithSardUnderscoreIsStillRedacted(t *testing.T) {
	if got, want := redactIfToken("/etc/sard_agent/x"), "/etc/"+redactedMark+"/x"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
