// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// The deep check of the repository has its own identifier; that it is not in
// the default run checks TestTheIdentifiersAreTheOnesOfTheList on a real run.
func TestTheIntegrityCheckHasItsOwnIdentifier(t *testing.T) {
	if hostcheck.CheckRepositoryIntegrity != "repository.integrity" {
		t.Errorf("CheckRepositoryIntegrity = %q", hostcheck.CheckRepositoryIntegrity)
	}
}
