// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"testing"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// docs/specs/proto/restic-keys.feature: an agent older than V3 sends fields 1-4
// only, and the server must read the missing flag as unknown.
func TestRepositoryInfoWithoutTheRecoveryKeyFieldIsParsedAsUnknown(t *testing.T) {
	var b []byte
	for n, v := range map[protowire.Number]string{1: "base", 2: "local", 3: "5e3f0a9c", 4: "restic-aes"} {
		b = protowire.AppendTag(b, n, protowire.BytesType)
		b = protowire.AppendString(b, v)
	}
	var info agentv1.RepositoryInfo
	if err := proto.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	if got := info.GetRecoveryKey(); got != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED {
		t.Errorf("recovery_key = %v, want RECOVERY_KEY_STATE_UNSPECIFIED", got)
	}
}

func TestRecoveryKeyStateSurvivesSerialization(t *testing.T) {
	for _, state := range []agentv1.RecoveryKeyState{
		agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_PRESENT,
		agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT,
	} {
		b, err := proto.Marshal(&agentv1.RepositoryInfo{Name: "base", RecoveryKey: state})
		if err != nil {
			t.Fatal(err)
		}
		var back agentv1.RepositoryInfo
		if err := proto.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if back.GetRecoveryKey() != state {
			t.Errorf("recovery_key = %v, want %v", back.GetRecoveryKey(), state)
		}
	}
}
