// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// UnknownRepository is the refusal for a name the config does not have; it
// lists the names that are there.
func UnknownRepository(name, configPath string, known []string) *refusal.Failure {
	if len(known) == 0 {
		return refusal.Fail(refusal.RepositoryUnknown, "no repositories configured in %s", configPath)
	}
	return refusal.Fail(refusal.RepositoryUnknown, "no repository named %q in %s; configured repositories: %s", name, configPath, strings.Join(known, ", "))
}
