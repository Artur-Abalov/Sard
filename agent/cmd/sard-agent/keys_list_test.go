// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// docs/specs/agent/restic-keys.feature, rule "key list показывает все ключи без паролей".

func TestTheKeyListShowsPurposeLabelHostTimeAndTheKeyOfThisHost(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH(), keyR(), keyV())
	code, stdout, _ := h.keyCmd("list", "base")
	assertCode(t, code, exitOK)
	rows := strings.Split(strings.TrimSpace(stdout), "\n")
	if got := strings.Fields(rows[0]); strings.Join(got, " ") != "ID PURPOSE LABEL HOST CREATED CURRENT" {
		t.Fatalf("header %q", rows[0])
	}
	if len(rows) != 4 {
		t.Fatalf("rows:\n%s", stdout)
	}
	byID := map[string][]string{}
	for _, r := range rows[1:] {
		f := strings.Fields(r)
		byID[f[0]] = f
	}
	v := byID[idOf(keyV())]
	if got := strings.Join(v, " "); got != idOf(keyV())+" verifier vrf-1 src1 2026-10-20T10:00:00Z" {
		t.Errorf("row V = %q", got)
	}
	if h := byID[idOf(keyH())]; h[len(h)-1] != "*" {
		t.Errorf("row H = %q, want the mark of the key of this host", h)
	}
	for _, id := range []string{idOf(keyR()), idOf(keyV())} {
		if f := byID[id]; f[len(f)-1] == "*" {
			t.Errorf("row %s is marked: %q", id, f)
		}
	}
}

func TestAKeyNotIssuedThroughSardIsShownWithoutPurpose(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, stdout, _ := h.keyCmd("list", "base")
	for _, row := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(row, idOf(keyH())) {
			if f := strings.Fields(row); f[1] != "-" || f[2] != "-" {
				t.Errorf("row H = %q", row)
			}
			return
		}
	}
	t.Fatalf("no row of H:\n%s", stdout)
}

// keyListJSON is the object of repo key list --json.
type keyListJSON struct {
	Repository string `json:"repository"`
	Keys       []struct {
		ID      string  `json:"id"`
		Purpose *string `json:"purpose"`
		Label   *string `json:"label"`
		Host    string  `json:"host"`
		Created string  `json:"created"`
		Current bool    `json:"current"`
	} `json:"keys"`
	Warnings []string `json:"warnings"`
}

func decodeList(t *testing.T, stdout string) keyListJSON {
	t.Helper()
	var got keyListJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return got
}

func TestTheKeyListInJSONForAutomation(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, stdout, stderr := h.keyCmd("list", "base", "--json")
	assertCode(t, code, exitOK)
	got := decodeList(t, stdout)
	if got.Repository != "base" || len(got.Keys) != 2 || got.Warnings == nil || len(got.Warnings) != 0 || stderr != "" {
		t.Fatalf("repository %q, %d keys, warnings %v, stderr %q", got.Repository, len(got.Keys), got.Warnings, stderr)
	}
	assertKeysOfFullShape(t, got)
}

// assertKeysOfFullShape: every key has a full id of 64 characters, a host and a time.
func assertKeysOfFullShape(t *testing.T, got keyListJSON) {
	t.Helper()
	for _, k := range got.Keys {
		if len(k.ID) != 64 || k.Host != "src1" || k.Created == "" {
			t.Errorf("key %+v", k)
		}
	}
}

func TestTheJSONOfTheKeyListNullsWhatSardDidNotIssue(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, stdout, _ := h.keyCmd("list", "base", "--json")
	got := decodeList(t, stdout)
	first, second := got.Keys[0], got.Keys[1]
	if first.ID != keyH().id || first.Purpose != nil || first.Label != nil || !first.Current {
		t.Errorf("H = %+v, want purpose and label null, current true", first)
	}
	if second.Current || second.Purpose == nil || *second.Purpose != "recovery" {
		t.Errorf("R = %+v", second)
	}
}

func TestTheKeyListIsAvailableToTheServiceUser(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, _ := h.asService("repo", "key", "list", "base")
	assertCode(t, code, exitOK)
}

func TestAnOutsiderDoesNotSeeTheKeyList(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, stderr := h.asOther("repo", "key", "list", "base")
	assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
	h.assertNoResticCalls()
}

func TestTheKeyListHoldsNoPasswords(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, stdout, stderr := h.keyCmd("list", "base", "--json")
	assertCode(t, code, exitOK)
	if strings.Contains(stdout+stderr, passMarker) {
		t.Fatalf("PASS-MARKER is in the output:\n%s\n%s", stdout, stderr)
	}
}

func TestASingleKeyIsAWarning(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH())
	code, _, stderr := h.keyCmd("list", "base")
	assertCode(t, code, exitOK)
	for _, want := range []string{"SINGLE_KEY", "only one key", "NO_RECOVERY_KEY", "sudo sard-agent repo key add base --for recovery"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestNoRecoveryKeyIsAWarning(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH(), keyV())
	_, _, stderr := h.keyCmd("list", "base")
	if !strings.Contains(stderr, "NO_RECOVERY_KEY") || strings.Contains(stderr, "SINGLE_KEY") {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

func TestWithARecoveryKeyAndASecondKeyThereAreNoWarnings(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, _, stderr := h.keyCmd("list", "base")
	if stderr != "" {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

func TestTheWarningsInJSONAreCodes(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH())
	code, stdout, _ := h.keyCmd("list", "base", "--json")
	assertCode(t, code, exitOK)
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || strings.Join(got.Warnings, ",") != "single_key,no_recovery_key" {
		t.Fatalf("warnings %v, %v", got.Warnings, err)
	}
}

func TestAStorageRefusalWhileReadingTheKeyList(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line   string
		code   int
		reason string
	}{
		{"Fatal: unable to open config file: Stat: Get \"https://s3.example.com/b/config\": dial tcp: lookup s3.example.com: no such host", exitTemporary, "BACKEND_UNAVAILABLE"},
		{"Fatal: unexpected response 418", exitAgentError, "BACKEND_REFUSED"},
	} {
		h := newKeysHost(t)
		h.base().keyring.fail = map[string]string{"list": c.line}
		code, _, stderr := h.keyCmd("list", "base")
		assertRefusal(t, code, stderr, c.code, c.reason)
	}
	h := newKeysHost(t)
	h.base().wrongPassword = true
	code, _, stderr := h.keyCmd("list", "base")
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
}

func TestTheKeyListWorksOnAVerifier(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.hostRole = verifierRole
	code, _, _ := h.keyCmd("list", "base")
	assertCode(t, code, exitOK)
}
