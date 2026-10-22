// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Scenario: Код причины по образцу принимается; Код причины вне образца не уходит на сервер как есть.
func TestAReasonCodeIsAnUpperCaseNameOfAtMost64Characters(t *testing.T) {
	tests := map[string]bool{
		"":                                false,
		"A":                               true,
		"TEST_1":                          true,
		strings.Repeat("A", 64):           true,
		strings.Repeat("A", 65):           false,
		"test_refused":                    false,
		"1TEST":                           false,
		"_TEST":                           false,
		"TEST-REFUSED":                    false,
		"TEST REFUSED":                    false,
		"TEST\n":                          false,
		"TEST\x00":                        false,
		"ТЕСТ":                            false,
		"A" + strings.Repeat("9", 63):     true,
		"A" + strings.Repeat("_Z9", 21):   true,
		"Z" + strings.Repeat("a", 1):      false,
		"A0123456789_ABCDEFGHIJKLMNOPQRZ": true,
	}
	for code, want := range tests {
		if got := sdk.ValidReason(code); got != want {
			t.Errorf("ValidReason(%q) = %v, want %v", code, got, want)
		}
	}
}

// Scenario: Стадия, которую задаёт плагин, подходит под образец.
func TestAStageIsALowerCaseWordWithHyphensOfAtMost32Characters(t *testing.T) {
	tests := map[string]bool{
		"":                           false,
		"check":                      true,
		"create-database":            true,
		"a":                          true,
		strings.Repeat("a", 32):      true,
		strings.Repeat("a", 33):      false,
		"Check":                      false,
		"1check":                     false,
		"-check":                     false,
		"check_it":                   false,
		"check it":                   false,
		"check\n":                    false,
		"a" + strings.Repeat("-", 5): true,
		"z":                          true,
		"az":                         true,
		"za-az":                      true,
		"{":                          false,
		"a{":                         false,
		"`a":                         false,
		"a`":                         false,
		"@":                          false,
	}
	for stage, want := range tests {
		if got := sdk.ValidStage(stage); got != want {
			t.Errorf("ValidStage(%q) = %v, want %v", stage, got, want)
		}
	}
}

func TestEveryGenericReasonOfTheSDKFitsThePatternAndIsListedOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range sdk.GenericReasons() {
		if !sdk.ValidReason(r) {
			t.Errorf("%q does not fit the pattern", r)
		}
		if seen[r] {
			t.Errorf("%q is listed twice", r)
		}
		seen[r] = true
	}
	for _, want := range []string{sdk.ReasonBackendUnavailable, sdk.ReasonWrongPassword, sdk.ReasonResticFailed, sdk.ReasonSnapshotUnknown, sdk.ReasonResticOutputUnexpected, sdk.ReasonCleanupFailed} {
		if !seen[want] {
			t.Errorf("%q is not a generic reason", want)
		}
	}
}

func TestARestoreErrorSaysReasonStageAndDetail(t *testing.T) {
	err := &sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "check", Detail: "no luck"}
	if got, want := err.Error(), "TEST_REFUSED (check): no luck"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestARestoreErrorKeepsItsCauseReachable(t *testing.T) {
	err := &sdk.RestoreError{Reason: "TEST_TOOL_MISSING", Stage: "check", Err: fmt.Errorf("tool %w", sdk.ErrProgramMissing)}
	if !errors.Is(fmt.Errorf("wrapped: %w", err), sdk.ErrProgramMissing) {
		t.Error("the cause of the error is not reachable")
	}
	if (&sdk.RestoreError{}).Unwrap() != nil {
		t.Error("an error without a cause unwraps to something")
	}
}

// Scenario: Ошибка хоста в потоке снимка проходит через плагин без изменений (the plugin only sets the stage).
func TestAtStageSetsTheStageOfARestoreErrorWithoutChangingTheOriginal(t *testing.T) {
	orig := &sdk.RestoreError{Reason: sdk.ReasonBackendUnavailable, Detail: "down", Retain: true}
	got := sdk.AtStage(fmt.Errorf("dump: %w", orig), "restore")
	var re *sdk.RestoreError
	if !errors.As(got, &re) || re.Stage != "restore" || re.Reason != orig.Reason || re.Detail != "down" || !re.Retain {
		t.Fatalf("AtStage = %#v", got)
	}
	if orig.Stage != "" {
		t.Error("the original error was changed")
	}
}

func TestAtStageLeavesOtherErrorsAndNilAlone(t *testing.T) {
	plain := errors.New("plain")
	if got := sdk.AtStage(plain, "restore"); got != plain {
		t.Errorf("AtStage(plain) = %v", got)
	}
	if got := sdk.AtStage(nil, "restore"); got != nil {
		t.Errorf("AtStage(nil) = %v", got)
	}
}

// Scenario: Разбор может переписать запись; Исход разбора становится результатом прерванной команды.
func TestRestoredIsACompletedRecoveryWithTheResult(t *testing.T) {
	done := sdk.RestoreResult{Database: "x1", Tables: 2}
	if r := sdk.Restored(done); r.Kind != sdk.RecoveryCompleted || r.Result != done {
		t.Errorf("Restored = %+v", r)
	}
}

func TestNothingRestoredIsTheZeroRecovery(t *testing.T) {
	if r := sdk.NothingRestored(); r.Kind != sdk.RecoveryNothingRestored || r.Retain {
		t.Errorf("NothingRestored = %+v", r)
	}
	if r := (sdk.Recovery{}); r.Kind != sdk.RecoveryNothingRestored {
		t.Errorf("the zero Recovery = %+v: it must mean that nothing was restored", r)
	}
}

func TestLeftBehindNamesWhatIsLeftWhyAndWhetherToKeepTheEntry(t *testing.T) {
	left := sdk.LeftBehind([]string{"thing a"}, "boom", true)
	if left.Kind != sdk.RecoveryLeftBehind || left.Leftover[0] != "thing a" || left.Cleanup != "boom" || !left.Retain {
		t.Errorf("LeftBehind = %+v", left)
	}
}
