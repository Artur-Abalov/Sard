// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"regexp"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// namePattern is Р2: 1-64 characters of A-Z a-z 0-9 - _, the first a
// letter or digit. No dot: a secret's file never equals a repository's
// restic-<name>.pass, and the name is never a path.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidName says whether the name may be given to a command that creates
// files named after it.
func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

// CheckName is the refusal NAME_INVALID for a name of the given kind
// ("secret" or "repository"), nil for a valid one.
func CheckName(kind, name string) *repoinit.Failure {
	if ValidName(name) {
		return nil
	}
	return repoinit.Fail(repoinit.NameInvalid, "%s name %q is not valid: use 1-64 characters from A-Z a-z 0-9 - _, the first a letter or digit", kind, name)
}
