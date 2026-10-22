// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

type checker struct{ sdk.Plugin }

func (checker) CheckPrograms(sdk.ProgramHost) sdk.ProgramsCheck {
	return sdk.ProgramsCheck{Status: sdk.ProgramsWarn, Message: "m"}
}

// Any plugin may check its programs on the host: the interface is optional,
// and a plugin that has no programs does not implement it.
func TestAPluginMayCheckItsProgramsOnTheHost(t *testing.T) {
	var p any = checker{}
	got, ok := p.(sdk.ProgramChecker)
	if !ok {
		t.Fatal("a plugin with CheckPrograms is not a ProgramChecker")
	}
	if c := got.CheckPrograms(nil); c.Status != sdk.ProgramsWarn || c.Message != "m" {
		t.Errorf("%+v", c)
	}
	if _, ok := any(struct{}{}).(sdk.ProgramChecker); ok {
		t.Error("everything is a ProgramChecker")
	}
}

func TestTheStatusesOfAProgramsCheckAreDistinctAndNotZero(t *testing.T) {
	seen := map[sdk.ProgramsStatus]bool{}
	for _, s := range []sdk.ProgramsStatus{sdk.ProgramsOK, sdk.ProgramsSkip, sdk.ProgramsWarn, sdk.ProgramsError} {
		if s == 0 || seen[s] {
			t.Errorf("status %d is zero or repeated", s)
		}
		seen[s] = true
	}
}
