// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Ч10: the identifiers of the checks of sard-agent check, in their order. A
// renamed or moved identifier fails this test.
func TestCheckIDsAreTheOnesOfTheSpecInTheirOrder(t *testing.T) {
	want := []string{
		"service.user", "config.load", "config.names", "config.references", "files.permissions", "restic.binary",
		"tls.identity", "tls.chain", "tls.expiry", "tls.role", "server.reachable", "server.tls", "server.accepted", "clock.skew",
		"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.locks",
		"secret.readable", "plugin.declared", "plugin.programs", "restore.locations", "selinux.labels", "selinux.denials", "service.running", "service.steps",
	}
	if got := hostcheck.CheckIDs(); !slices.Equal(got, want) || len(want) != 28 {
		t.Errorf("CheckIDs\n%v\nwant\n%v", got, want)
	}
}

// The deep check of the repository is not one of the 28 of the default run.
func TestTheIntegrityCheckHasItsOwnIdentifier(t *testing.T) {
	if hostcheck.CheckRepositoryIntegrity != "repository.integrity" {
		t.Errorf("CheckRepositoryIntegrity = %q", hostcheck.CheckRepositoryIntegrity)
	}
	if slices.Contains(hostcheck.CheckIDs(), hostcheck.CheckRepositoryIntegrity) {
		t.Error("the integrity check is not in the default list")
	}
}
