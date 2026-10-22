// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package refusal

import "testing"

// docs/specs/agent/restic-keys.feature, "Новые причины": the classes of the
// reasons of the restic key commands.
func TestTheReasonsOfTheKeyCommandsHaveTheClassesOfTheSpecification(t *testing.T) {
	want := map[Reason]Class{
		KeyPurposeMissing: ClassUsage, KeyPurposeInvalid: ClassUsage, KeyLabelRequired: ClassUsage,
		LabelInvalid: ClassUsage, KeyExists: ClassUsage, KeyOutputMissing: ClassUsage,
		KeyFileExists: ClassUsage, KeyFileRejected: ClassUsage, KeyIDAmbiguous: ClassUsage,
		KeyInUse: ClassUsage, LastKey: ClassUsage, ConfirmationRequired: ClassUsage,
		ConfirmationRejected: ClassUsage, RecoveryKeyOutputMissing: ClassUsage, VerifierReadOnly: ClassUsage,
		RecoveryKeyFailed: ClassAgentError, KeyFileWrite: ClassWrite,
	}
	for reason, class := range want {
		if got := ClassOf(reason); got != class {
			t.Errorf("%s: class %d, want %d", reason, got, class)
		}
	}
	if string(KeyIDAmbiguous) != "KEY_ID_AMBIGUOUS" || string(RecoveryKeyOutputMissing) != "RECOVERY_KEY_OUTPUT_MISSING" {
		t.Errorf("names %q %q", KeyIDAmbiguous, RecoveryKeyOutputMissing)
	}
}
