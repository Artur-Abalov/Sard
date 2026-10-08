// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// The class of every reason of the host-setup commands (A8a, the table of
// classes in docs/specs/agent/host-setup.feature).
func TestHostSetupReasonsHaveTheirClass(t *testing.T) {
	cases := map[refusal.Reason]refusal.Class{
		refusal.PrivilegesRequired:   refusal.ClassUsage,
		refusal.ServiceUserUnknown:   refusal.ClassUsage,
		refusal.NameInvalid:          refusal.ClassUsage,
		refusal.DefinedInConfig:      refusal.ClassUsage,
		refusal.PathInUse:            refusal.ClassUsage,
		refusal.SecretSourceMissing:  refusal.ClassUsage,
		refusal.SecretSourceConflict: refusal.ClassUsage,
		refusal.SecretEmpty:          refusal.ClassUsage,
		refusal.SecretTooLarge:       refusal.ClassUsage,
		refusal.SecretMismatch:       refusal.ClassUsage,
		refusal.BackendNotSupported:  refusal.ClassUsage,
		refusal.LocalPathInvalid:     refusal.ClassUsage,
		refusal.RevealRequired:       refusal.ClassUsage,
		refusal.RepositoryConflict:   refusal.ClassExists,
		refusal.ConfigLocked:         refusal.ClassTemporary,
		refusal.ConfigWrite:          refusal.ClassWrite,
		refusal.ServiceRestartFailed: refusal.ClassAgentError,
	}
	for reason, class := range cases {
		f := refusal.Fail(reason, "detail %d", 7)
		if f.Class != class || f.Reason != reason || f.Detail != "detail 7" {
			t.Errorf("%s: class %v, detail %q, want class %v", reason, f.Class, f.Detail, class)
		}
	}
}

func TestFailureErrorIsReasonAndDetail(t *testing.T) {
	f := refusal.Fail(refusal.NameInvalid, "bad %q", "x")
	if got := f.Error(); got != `NAME_INVALID: bad "x"` {
		t.Fatalf("Error() = %q", got)
	}
}

func TestAFailureWithoutAReasonIsItsDetailAlone(t *testing.T) {
	f := &refusal.Failure{Class: refusal.ClassUsage, Detail: "reading config x: boom"}
	if f.Error() != "reading config x: boom" {
		t.Fatalf("Error() = %q", f.Error())
	}
}
