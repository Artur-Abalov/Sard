// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/role"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// R2b: the restore of postgresql from the console is done by the restore
// engine of the host; a host without one rejects the step and starts nothing.
func TestRestoreByThePostgresqlPluginIsRejectedOnAHostWithoutConsoleRestore(t *testing.T) {
	r := newRig(t)
	s := step(js(k()))
	s.Action, s.SnapshotId = agentv1.Action_ACTION_RESTORE, "abc"
	res := r.run(s)
	want(t, res, rejected)
	mentions(t, res.GetMessage(), "restore from the console is not enabled")
	if len(r.restic.run) != 0 || r.proc.started() != 0 {
		t.Errorf("restic ran %d times, processes %d", len(r.restic.run), r.proc.started())
	}
	noOutput(t, res)
}

// Scenario: Проверка и запуск скрипта плагином postgresql отклоняются.
func TestVerifyAndRunByThePostgresqlPluginAreRejected(t *testing.T) {
	roles := map[agentv1.Action]role.Role{agentv1.Action_ACTION_VERIFY: role.Verifier, agentv1.Action_ACTION_RUN: role.Source}
	for name, action := range map[string]agentv1.Action{"ACTION_VERIFY": agentv1.Action_ACTION_VERIFY, "ACTION_RUN": agentv1.Action_ACTION_RUN} {
		r := newRigAs(t, roles[action])
		s := step(js(k()))
		s.Action, s.SnapshotId = action, "abc"
		res := r.run(s)
		want(t, res, rejected)
		mentions(t, res.GetMessage(), "postgresql", action.String())
		if r.proc.started() != 0 {
			t.Errorf("%s: processes started: %d", name, r.proc.started())
		}
	}
}

// Scenario: Шаг с неизвестным репозиторием отклоняется.
func TestStepWithAnUnknownRepositoryIsRejected(t *testing.T) {
	r := newRig(t)
	s := step(js(k()))
	s.RepositoryName = "nope"
	want(t, r.run(s), rejected)
	if r.proc.started() != 0 {
		t.Errorf("processes started: %d", r.proc.started())
	}
}
