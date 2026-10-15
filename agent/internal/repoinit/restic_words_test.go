// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Ч3: a restic that prints what restic does not (a certificate, ssh) is told
// from the generic refusals by its words.
func TestReclassifyGivesARefusalTheReasonItsWordsCallFor(t *testing.T) {
	cases := []struct {
		name   string
		reason refusal.Reason
		detail string
		want   refusal.Reason
	}{
		{"certificate", refusal.BackendRefused, "Stat: tls: failed to verify certificate: X509 certificate signed by unknown authority", refusal.BackendUnavailable},
		{"certificate of a denied storage", refusal.StorageAccessDenied, "x509: bad", refusal.StorageAccessDenied},
		{"ssh key", refusal.BackendRefused, "subprocess ssh: Permission denied (publickey).", refusal.SSHKeyNotAuthorized},
		{"ssh key as access denied", refusal.StorageAccessDenied, "Permission denied (publickey,password)", refusal.SSHKeyNotAuthorized},
		{"host key", refusal.BackendRefused, "Host key verification failed.", refusal.HostKeyMismatch},
		{"host key as access denied", refusal.StorageAccessDenied, "host key verification failed", refusal.HostKeyMismatch},
		{"ssh key of another reason", refusal.WrongPassword, "permission denied (publickey", refusal.WrongPassword},
		{"other words", refusal.BackendRefused, "something else", refusal.BackendRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := refusal.Fail(tc.reason, "%s", tc.detail)
			got := repoinit.Reclassify(f)
			if got.Reason != tc.want || got.Detail != tc.detail {
				t.Errorf("Reclassify = %s %q, want %s %q", got.Reason, got.Detail, tc.want, tc.detail)
			}
		})
	}
}

func TestCannotLockIsResticSayingItsLockWasDeniedForTheCredentials(t *testing.T) {
	cases := map[string]bool{
		"Fatal: unable to create lock in backend: client.PutObject: Access Denied.": true,
		"unable to create lock: 403 Forbidden":                                      true,
		"UNABLE TO CREATE LOCK: Permission denied":                                  true,
		"unable to create lock in backend: connection reset":                        false,
		"Access Denied.":                                                            false,
		"": false,
	}
	for text, want := range cases {
		if got := repoinit.CannotLock(text); got != want {
			t.Errorf("CannotLock(%q) = %v, want %v", text, got, want)
		}
	}
}

// restic check ends with exit code 1 for the errors it found in the
// repository, not for a storage it could not reach.
func TestFoundErrorsIsExitCodeOneThatIsNotANetworkFailure(t *testing.T) {
	exit := func(code int) *restic.ExitError { return &restic.ExitError{Code: code, Message: "x"} }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"exit 1", fmt.Errorf("restic check: %w", exit(1)), true},
		{"exit 2", fmt.Errorf("restic check: %w", exit(2)), false},
		{"network", fmt.Errorf("restic check: %w: %w", restic.ErrNetwork, exit(1)), false},
		{"not an exit", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := repoinit.FoundErrors(tc.err); got != tc.want {
			t.Errorf("%s: FoundErrors = %v, want %v", tc.name, got, tc.want)
		}
	}
}
