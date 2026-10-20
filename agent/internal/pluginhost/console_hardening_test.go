// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// badRestoreSchema is a plugin whose restore config schema does not compile.
type badRestoreSchema struct{ restorer }

func (*badRestoreSchema) RestoreConfigSchema() []byte { return []byte(`{"type": 5}`) }

func TestAPluginWhoseRestoreSchemaDoesNotCompileIsNotAccepted(t *testing.T) {
	if _, err := pluginhost.NewSource(&badRestoreSchema{}, pluginhost.NewSecrets(nil, nil)); err == nil {
		t.Fatal("NewSource accepted a restore schema that does not compile")
	}
}

func TestAHostWithoutRestoreFromTheConsoleRejectsARestoreStep(t *testing.T) {
	f := newHandlers(t, &restorer{})
	_, err := f.run(t, restoreStep(restoreConfigD))
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "restore from the console is not enabled on this host") {
		t.Fatalf("err = %v", err)
	}
}

// The failure of the plugin to plan is its own, not a violation of the rules of a plan.
func TestAPluginThatCannotPlanTheRestoreIsHeardNotJudged(t *testing.T) {
	h := newConsoleHost(t, &restorer{plan: func(sdk.Config) (sdk.RestorePlan, error) {
		return sdk.RestorePlan{}, errors.New("the plugin has no idea")
	}})
	_, err := h.run(t, restoreStep(restoreConfigD))
	if err == nil || !strings.Contains(err.Error(), "the plugin has no idea") || errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

// 64 characters of 0-9 and a-f and nothing else name a snapshot.
func TestOnlyLowerCaseHexadecimalDigitsNameASnapshot(t *testing.T) {
	tests := []struct {
		name  string
		digit string
		valid bool
	}{
		{"zeros", "0", true}, {"nines", "9", true}, {"a", "a", true}, {"f", "f", true},
		{"just below 0", "/", false}, {"just above 9", ":", false},
		{"just below a", "`", false}, {"just above f", "g", false}, {"capital", "A", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newConsoleHost(t, &restorer{})
			st := restoreStep(restoreConfigD)
			st.SnapshotId = strings.Repeat(tt.digit, 64)
			_, err := h.run(t, st)
			refused := errors.Is(err, executor.ErrRejected) && strings.Contains(err.Error(), "snapshot_id")
			if refused == tt.valid {
				t.Errorf("refused = %v (%v), want %v", refused, err, !tt.valid)
			}
		})
	}
}
