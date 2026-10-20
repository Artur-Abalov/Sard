// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package journaltest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func TestThePluginIsCalledJournaltestUnlessTheTestNamesIt(t *testing.T) {
	if got := (&journaltest.Plugin{}).Name(); got != "journaltest" {
		t.Errorf("Name() = %q", got)
	}
	if got := (&journaltest.Plugin{PluginName: "other"}).Name(); got != "other" {
		t.Errorf("Name() = %q", got)
	}
}

func TestWithoutAScriptThePluginPlansNothingRestoresNothingAndRecoversNothing(t *testing.T) {
	p := &journaltest.Plugin{}
	plan, err := p.PlanRestore(sdk.Config{})
	if err != nil || plan.Destination != "" {
		t.Errorf("PlanRestore = %+v, %v", plan, err)
	}
	res, err := p.Restore(context.Background(), nil, sdk.Config{}, sdk.RestoreRequest{})
	if err != nil || res != (sdk.RestoreResult{}) {
		t.Errorf("Restore = %+v, %v", res, err)
	}
	if rec := p.Recover(context.Background(), nil, "c1", nil); rec.Kind != sdk.RecoveryNothingRestored {
		t.Errorf("Recover = %+v", rec)
	}
}

func TestThePluginDoesWhatTheTestScripts(t *testing.T) {
	boom := errors.New("boom")
	p := &journaltest.Plugin{
		Plan: func(sdk.Config) (sdk.RestorePlan, error) { return sdk.RestorePlan{}, boom },
		RestoreFn: func(_ context.Context, _ sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
			return sdk.RestoreResult{Target: req.CommandID}, boom
		},
		RecoverFn: func(_ context.Context, _ sdk.RecoveryHost, id string, entry []byte) sdk.Recovery {
			return sdk.LeftBehind([]string{id, string(entry)}, "why", true)
		},
	}
	if _, err := p.PlanRestore(sdk.Config{}); !errors.Is(err, boom) {
		t.Errorf("PlanRestore: %v", err)
	}
	res, err := p.Restore(context.Background(), nil, sdk.Config{}, sdk.RestoreRequest{CommandID: "c1"})
	if !errors.Is(err, boom) || res.Target != "c1" {
		t.Errorf("Restore = %+v, %v", res, err)
	}
	rec := p.Recover(context.Background(), nil, "c1", []byte("e1"))
	if rec.Kind != sdk.RecoveryLeftBehind || rec.Leftover[1] != "e1" || !rec.Retain {
		t.Errorf("Recover = %+v", rec)
	}
}
