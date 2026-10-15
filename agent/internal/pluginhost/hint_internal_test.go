// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// F1: the agent knows a missing program of any plugin by the SDK error, not by
// the words of one plugin.
func TestAMissingProgramIsKnownByTheSDKErrorNotByItsWords(t *testing.T) {
	byWords := errors.New("pg_dump not found in the PATH of the agent")
	if got := withCheckHint(byWords); got != byWords {
		t.Errorf("a message that only reads like a missing program got the hint: %q", got)
	}
	bySentinel := fmt.Errorf("mydb: %w: %q", sdk.ErrProgramMissing, "mongodump")
	got := withCheckHint(bySentinel)
	if !strings.HasSuffix(got.Error(), "\n"+checkHint) || !errors.Is(got, sdk.ErrProgramMissing) {
		t.Errorf("a plugin's missing program got no hint: %q", got)
	}
}
