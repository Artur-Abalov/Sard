// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func TestTheMessagesOfEachRefusalAreTheOnesTheOperatorIsPromised(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"empty password", exitErr("an empty password is not allowed", restic.ErrEmptyPassword),
			"PASSWORD_FILE_EMPTY: restic refuses an empty password: the password file /etc/sard/main.pass holds no password"},
		{"no id", fmt.Errorf("x: %w", restic.ErrBadOutput),
			"RESTIC_OUTPUT_UNEXPECTED: restic printed no repository id; check the repository with `sard-agent repo list`"},
		{"exit without a message", exitErr("", nil),
			"BACKEND_REFUSED: backend s3: restic cat: exit code 1"},
	}
	for _, c := range cases {
		if got := repoinit.FromRestic(t.Context(), c.err, target).Error(); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestAnExistingRepositoryIsReportedWithItsIDAfterTheExplanation(t *testing.T) {
	_, f := repoinit.Create(t.Context(), &fakeRepo{idErrs: []error{nil}, id: "abc"}, target)
	want := `REPOSITORY_EXISTS: a repository is already initialised at the address of "main"; nothing was changed (repository_id abc)`
	if f.Error() != want {
		t.Fatalf("%q", f.Error())
	}
}

func TestATimeoutOrInterruptNamesTheBackendAndWarnsOfAPartialRepository(t *testing.T) {
	const partial = "; the repository may have been created partially, run the command again"
	cases := []struct {
		name  string
		cause error
		want  string
	}{
		{"timeout", repoinit.ErrTimeout, "TIMEOUT: the command ran out of time (--timeout) while working with the backend s3"},
		{"interrupt", context.Canceled, "INTERRUPTED: the command was interrupted while working with the backend s3"},
	}
	for _, c := range cases {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(c.cause)
		if f := repoinit.FromRestic(ctx, errors.New("restic init: killed"), target); f.Error() != c.want {
			t.Errorf("%s: %q", c.name, f.Error())
		}
		ctx2, cancel2 := context.WithCancelCause(t.Context())
		r := &cancellingRepo{cancel: func() { cancel2(c.cause) }}
		if _, f := repoinit.Create(ctx2, r, target); f.Error() != c.want+partial {
			t.Errorf("%s from init: %q", c.name, f.Error())
		}
	}
}

func TestACancelledCommandNeverTakesResticsAnswerForANormalOne(t *testing.T) {
	noRepo := fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	exists := fmt.Errorf("restic init: %w", restic.ErrRepositoryExists)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok, f := repoinit.Inspect(ctx, &fakeRepo{idErrs: []error{noRepo}}, target); ok || f == nil || f.Reason != refusal.Interrupted {
		t.Errorf("inspect: %v %v", ok, f)
	}
	live, cancelLive := context.WithCancel(t.Context())
	r := &cancelOnInit{fakeRepo: &fakeRepo{idErrs: []error{noRepo, nil}, id: "abc", initErr: exists}, cancel: cancelLive}
	if _, f := repoinit.Create(live, r, target); f == nil || f.Reason != refusal.Interrupted || f.ID != "" {
		t.Errorf("create: %+v", f)
	}
}

// cancelOnInit cancels the context as init runs and fails as restic does
// for a repository that exists.
type cancelOnInit struct {
	*fakeRepo
	cancel func()
}

func (c *cancelOnInit) Init(ctx context.Context) (string, error) {
	c.cancel()
	return c.fakeRepo.Init(ctx)
}

func TestInspectRefusesAnAnswerThatIsNotAMissingRepository(t *testing.T) {
	id, ok, f := repoinit.Inspect(t.Context(), &fakeRepo{idErrs: []error{exitErr("Access Denied", nil)}}, target)
	if id != "" || ok || f == nil || f.Reason != refusal.BackendRefused {
		t.Fatalf("%q %v %v", id, ok, f)
	}
}

func TestCreateStopsAtAFailedInspectionWithoutInitialising(t *testing.T) {
	r := &fakeRepo{idErrs: []error{exitErr("Access Denied", nil)}, initID: "new"}
	id, f := repoinit.Create(t.Context(), r, target)
	if id != "" || f == nil || f.Reason != refusal.BackendRefused || r.initRuns != 0 {
		t.Fatalf("%q %v %d", id, f, r.initRuns)
	}
}

func TestScrubberMasksASecretThatEndsTheText(t *testing.T) {
	scrub := repoinit.Scrubber("", nil, "SECRET")
	if got := scrub("key SECRET"); got != "key [REDACTED]" {
		t.Errorf("%q", got)
	}
	if got := scrub("almost SEC"); got != "almost SEC" {
		t.Errorf("%q", got)
	}
}

func TestPreflightOfARepositoryWithoutAnEnvFileLooksAtNoOtherFile(t *testing.T) {
	r := repo
	r.EnvFile = ""
	checked, f := repoinit.Preflight(goodHost().host(), r, 0, false)
	if f != nil || checked.EnvAssignments != nil || checked.PasswordMissing {
		t.Fatalf("%+v, %v", checked, f)
	}
}
