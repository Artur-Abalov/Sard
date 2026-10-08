// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func TestFromCheckExplainsEachProblemOfTheResticBinary(t *testing.T) {
	const bin, cfg = "/opt/sard/restic", "/etc/sard/agent.yaml"
	minimum := restic.Minimum.String()
	cases := []struct {
		name       string
		ce         *restic.CheckError
		configured bool
		reason     refusal.Reason
		contains   []string
	}{
		{"not found at the configured path", &restic.CheckError{Problem: restic.NotFound, Binary: bin, Err: fs.ErrNotExist}, true,
			refusal.ResticNotFound, []string{bin, "restic.path in " + cfg, minimum}},
		{"not found next to the binary", &restic.CheckError{Problem: restic.NotFound, Binary: bin, Err: fs.ErrNotExist}, false,
			refusal.ResticNotFound, []string{bin, "next to the sard-agent binary", "restic.path is not set", cfg, minimum}},
		{"too old", &restic.CheckError{Problem: restic.TooOld, Binary: bin, Found: "0.18.1", Minimum: restic.Minimum}, true,
			refusal.ResticTooOld, []string{"0.18.1", bin, minimum, restic.Pinned.String(), cfg}},
		{"unusable", &restic.CheckError{Problem: restic.Unusable, Binary: bin, Err: errors.New("exit code 1")}, false,
			refusal.ResticUnusable, []string{bin, "cannot be used as restic", "exit code 1"}},
	}
	for _, c := range cases {
		f := repoinit.FromCheck(c.ce, c.configured, cfg)
		if f.Reason != c.reason || f.Class != refusal.ClassAgentError {
			t.Errorf("%s: reason %v class %v", c.name, f.Reason, f.Class)
		}
		for _, want := range c.contains {
			if !strings.Contains(f.Detail, want) {
				t.Errorf("%s: %q does not contain %q", c.name, f.Detail, want)
			}
		}
	}
	// the wording differs between the two NotFound cases
	a := repoinit.FromCheck(cases[0].ce, true, cfg).Detail
	b := repoinit.FromCheck(cases[0].ce, false, cfg).Detail
	if a == b {
		t.Error("configured and default NotFound read the same")
	}
}
