// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// V3 (K16): repository.keys.
func TestKeysLine(t *testing.T) {
	cases := []struct {
		name         string
		total        int
		warnings     []string
		status       hostcheck.Status
		message      string
		hintRequired bool
	}{
		{"a recovery key and another key", 2, nil, hostcheck.OK, "2 keys, recovery key present", false},
		{"only the recovery key", 1, nil, hostcheck.OK, "1 key, recovery key present", false},
		{"no recovery key", 2, []string{restickeys.WarnNoRecoveryKey}, hostcheck.Warn, "no recovery key", true},
		{"no recovery key and one key", 1, []string{restickeys.WarnSingleKey, restickeys.WarnNoRecoveryKey}, hostcheck.Warn, "no recovery key; only one key", true},
	}
	for _, c := range cases {
		l := hostcheck.KeysLine("base", c.total, c.warnings)
		hint := ""
		if c.hintRequired {
			hint = "sudo sard-agent repo key add base --for recovery"
		}
		if l.Check != "repository.keys" || l.Subject != "base" || l.Status != c.status || l.Message != c.message || l.Hint != hint {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

func TestTheKeyStepIsBetweenTheIDAndTheLocks(t *testing.T) {
	want := []string{"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks"}
	if got := hostcheck.RepositoryChecks(); !slices.Equal(got, want) {
		t.Errorf("checks %v, want %v", got, want)
	}
}
