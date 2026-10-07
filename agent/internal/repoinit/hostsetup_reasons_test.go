// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// The class of every reason of the host-setup commands (A8a, the table of
// classes in docs/specs/agent/host-setup.feature).
func TestHostSetupReasonsHaveTheirClass(t *testing.T) {
	cases := map[repoinit.Reason]repoinit.Class{
		repoinit.PrivilegesRequired:   repoinit.ClassUsage,
		repoinit.ServiceUserUnknown:   repoinit.ClassUsage,
		repoinit.NameInvalid:          repoinit.ClassUsage,
		repoinit.DefinedInConfig:      repoinit.ClassUsage,
		repoinit.PathInUse:            repoinit.ClassUsage,
		repoinit.SecretSourceMissing:  repoinit.ClassUsage,
		repoinit.SecretSourceConflict: repoinit.ClassUsage,
		repoinit.SecretEmpty:          repoinit.ClassUsage,
		repoinit.SecretTooLarge:       repoinit.ClassUsage,
		repoinit.SecretMismatch:       repoinit.ClassUsage,
		repoinit.BackendNotSupported:  repoinit.ClassUsage,
		repoinit.LocalPathInvalid:     repoinit.ClassUsage,
		repoinit.RevealRequired:       repoinit.ClassUsage,
		repoinit.RepositoryConflict:   repoinit.ClassExists,
		repoinit.ConfigLocked:         repoinit.ClassTemporary,
		repoinit.ConfigWrite:          repoinit.ClassWrite,
		repoinit.ServiceRestartFailed: repoinit.ClassAgentError,
	}
	for reason, class := range cases {
		f := repoinit.Fail(reason, "detail %d", 7)
		if f.Class != class || f.Reason != reason || f.Detail != "detail 7" {
			t.Errorf("%s: class %v, detail %q, want class %v", reason, f.Class, f.Detail, class)
		}
	}
}

func TestFailureErrorIsReasonAndDetail(t *testing.T) {
	f := repoinit.Fail(repoinit.NameInvalid, "bad %q", "x")
	if got := f.Error(); got != `NAME_INVALID: bad "x"` {
		t.Fatalf("Error() = %q", got)
	}
}
