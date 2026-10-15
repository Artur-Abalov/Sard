// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// A restic that prints what restic does not (a certificate, ssh) is told
// from the generic refusals by its words (Ч3).
var refusalsByWords = []struct {
	words string
	from  []refusal.Reason
	to    refusal.Reason
}{
	{"x509", []refusal.Reason{refusal.BackendRefused}, refusal.BackendUnavailable},
	{"permission denied (publickey", []refusal.Reason{refusal.BackendRefused, refusal.StorageAccessDenied}, refusal.SSHKeyNotAuthorized},
	{"host key verification failed", []refusal.Reason{refusal.BackendRefused, refusal.StorageAccessDenied}, refusal.HostKeyMismatch},
}

// Reclassify gives a refusal the reason its words call for.
func Reclassify(f *refusal.Failure) *refusal.Failure {
	text := strings.ToLower(f.Detail)
	for _, c := range refusalsByWords {
		if strings.Contains(text, c.words) && slices.Contains(c.from, f.Reason) {
			return refusal.Fail(c.to, "%s", f.Detail)
		}
	}
	return f
}

// FoundErrors: restic check ended with exit code 1, which it uses for the
// errors it found in the repository (not for a storage it could not reach).
func FoundErrors(err error) bool {
	var exit *restic.ExitError
	return errors.As(err, &exit) && exit.Code == 1 && !errors.Is(err, restic.ErrNetwork)
}

// CannotLock: restic could not create its lock because the credentials may
// not write.
func CannotLock(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "unable to create lock") &&
		(strings.Contains(text, "access denied") || strings.Contains(text, "forbidden") || strings.Contains(text, "permission denied"))
}
