// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

// Direct, white-box tests of enroll_report.go's message-building helpers:
// each field the caller (dialAndEnroll, lockForWriting, ...) never sets for
// a given *enroll.Error must contribute nothing to the message, and each
// field it does set must actually appear — the full-CLI @fake tests
// (enroll_fake_test.go) exercise these through real scenarios, but cannot
// cheaply reach every combination of fields set/unset that these unit
// tests check directly.

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func TestReasonMeaningSuffixIsEmptyForClassAgentErrorRegardlessOfReason(t *testing.T) {
	// A contrived state (ClassAgentError normally carries CSR_INVALID or
	// HOSTNAME_INVALID, not TOKEN_USED) proves the Class check itself
	// suppresses reasonMeaning, not merely that TOKEN_USED usually
	// coincides with a different class.
	e := &enroll.Error{Class: enroll.ClassAgentError, Reason: "TOKEN_USED"}
	if got := reasonMeaningSuffix(e); got != "" {
		t.Fatalf("reasonMeaningSuffix = %q, want empty for ClassAgentError", got)
	}
}

func TestReasonMeaningSuffixAddsTheMeaningWhenOneExists(t *testing.T) {
	e := &enroll.Error{Class: enroll.ClassTokenRefused, Reason: "TOKEN_USED"}
	got := reasonMeaningSuffix(e)
	if !strings.Contains(got, "already been used") {
		t.Fatalf("reasonMeaningSuffix = %q, want it to contain TOKEN_USED's meaning", got)
	}
}

func TestReasonMeaningSuffixIsEmptyWhenNoMeaningExists(t *testing.T) {
	e := &enroll.Error{Class: enroll.ClassTrust, Reason: "TOKEN_FOREIGN_CA"}
	if got := reasonMeaningSuffix(e); got != "" {
		t.Fatalf("reasonMeaningSuffix = %q, want empty (TOKEN_FOREIGN_CA has no reasonMeanings entry)", got)
	}
}

func TestAddressSuffixIsEmptyWithoutAnAddress(t *testing.T) {
	if got := addressSuffix(&enroll.Error{}); got != "" {
		t.Fatalf("addressSuffix = %q, want empty", got)
	}
}

func TestAddressSuffixNamesTheAddressWhenSet(t *testing.T) {
	got := addressSuffix(&enroll.Error{Address: "sard.example.com:9090"})
	if !strings.Contains(got, "sard.example.com:9090") {
		t.Fatalf("addressSuffix = %q, want it to name the address", got)
	}
}

func TestNamesSuffixIsEmptyWithoutNames(t *testing.T) {
	if got := namesSuffix(&enroll.Error{}); got != "" {
		t.Fatalf("namesSuffix = %q, want empty", got)
	}
}

func TestNamesSuffixListsExactlyOneName(t *testing.T) {
	got := namesSuffix(&enroll.Error{Names: []string{"sard.example.com"}})
	if !strings.Contains(got, "sard.example.com") {
		t.Fatalf("namesSuffix = %q, want it to list the one name", got)
	}
}

func TestNamesSuffixListsMultipleNames(t *testing.T) {
	got := namesSuffix(&enroll.Error{Names: []string{"a.example.com", "b.example.com"}})
	if !strings.Contains(got, "a.example.com") || !strings.Contains(got, "b.example.com") {
		t.Fatalf("namesSuffix = %q, want both names listed", got)
	}
}

func TestCodeSuffixIsEmptyWithoutACode(t *testing.T) {
	if got := codeSuffix(&enroll.Error{}); got != "" {
		t.Fatalf("codeSuffix = %q, want empty", got)
	}
}

func TestCodeSuffixNamesTheCodeWhenSet(t *testing.T) {
	got := codeSuffix(&enroll.Error{Code: "Unavailable"})
	if !strings.Contains(got, "Unavailable") {
		t.Fatalf("codeSuffix = %q, want it to name the gRPC code", got)
	}
}

// reportEnrollError's !errors.As branch handles a plain error that is not
// (even wrapping) an *enroll.Error — defensively, since every production
// caller today only ever passes one, but nothing enforces that; this is
// the only way to exercise it.
func TestReportEnrollErrorHandlesANonEnrollError(t *testing.T) {
	var out strings.Builder
	plain := errors.New("boom")
	code := reportEnrollError(&out, plain)
	if code != exitAgentError {
		t.Fatalf("code = %d, want exitAgentError", code)
	}
	if !strings.Contains(out.String(), "boom") {
		t.Fatalf("stderr = %q, want it to contain the error text", out.String())
	}
}

// enrollHelpCodes must actually include every enrollClassCodes row, not
// just the two rows (0, identity-exists) added around it — checked against
// a fixed, independent count, not enrollHelpCodes() comparing itself.
func TestEnrollHelpCodesHasExactlyEightRows(t *testing.T) {
	got := enrollHelpCodes()
	if len(got) != 8 {
		t.Fatalf("enrollHelpCodes() has %d rows, want 8 (6 classes + success + identity-exists)", len(got))
	}
	wantWords := []string{"success", "agent error", "usage", "token refused", "trust", "temporary", "write", "identity exists"}
	for _, w := range wantWords {
		found := false
		for _, c := range got {
			if c.word == w {
				found = true
			}
		}
		if !found {
			t.Errorf("enrollHelpCodes() is missing the %q row", w)
		}
	}
}
