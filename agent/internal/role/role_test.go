// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package role_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/role"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func TestWhatEachRoleMayRun(t *testing.T) {
	allowed := map[role.Role][]agentv1.Action{
		role.Source:   {agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN, agentv1.Action_ACTION_FORGET, agentv1.Action_ACTION_PRUNE},
		role.Verifier: {agentv1.Action_ACTION_VERIFY},
	}
	for r, ok := range allowed {
		for _, a := range []agentv1.Action{agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN, agentv1.Action_ACTION_VERIFY, agentv1.Action_ACTION_FORGET, agentv1.Action_ACTION_PRUNE} {
			want := false
			for _, x := range ok {
				want = want || x == a
			}
			if got := r.Allows(a); got != want {
				t.Errorf("%s.Allows(%s) = %v", r, a, got)
			}
		}
	}
}

func TestUnspecifiedAndUnknownActionsPassTheRoleGate(t *testing.T) {
	for _, r := range []role.Role{role.Source, role.Verifier} {
		for _, a := range []agentv1.Action{agentv1.Action_ACTION_UNSPECIFIED, agentv1.Action(99)} {
			if !r.Allows(a) {
				t.Errorf("%s.Allows(%d) = false", r, a)
			}
		}
	}
}

func TestOnlyTheTwoRolesAreValid(t *testing.T) {
	for r, want := range map[role.Role]bool{role.Source: true, role.Verifier: true, "": false, "admin": false, "Source": false} {
		if got := r.Valid(); got != want {
			t.Errorf("%q.Valid() = %v", r, got)
		}
	}
}
