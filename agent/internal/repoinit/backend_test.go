// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// fakeRepo answers ID and Init with the given errors; the other methods of
// restic.Repository are not used by repoinit.
type fakeRepo struct {
	idErrs   []error // one per ID call
	id       string
	initID   string
	initErr  error
	idCalls  int
	initRuns int
	restic.Repository
}

func (f *fakeRepo) ID(context.Context) (string, error) {
	err := f.idErrs[min(f.idCalls, len(f.idErrs)-1)]
	f.idCalls++
	if err != nil {
		return "", err
	}
	return f.id, nil
}

func (f *fakeRepo) Init(context.Context) (string, error) {
	f.initRuns++
	return f.initID, f.initErr
}

var target = repoinit.Target{
	Name: "main", Backend: "s3", PasswordFile: "/etc/sard/main.pass",
	Scrub: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[x]") },
}

func exitErr(msg string, kind error) error {
	exit := &restic.ExitError{Code: 1, Message: msg}
	if kind != nil {
		return fmt.Errorf("restic cat: %w: %w", kind, exit)
	}
	return fmt.Errorf("restic cat: %w", exit)
}

func TestInspectSaysWhetherTheRepositoryIsInitialised(t *testing.T) {
	id, ok, f := repoinit.Inspect(t.Context(), &fakeRepo{idErrs: []error{nil}, id: "abc"}, target)
	if id != "abc" || !ok || f != nil {
		t.Fatalf("initialised: %q %v %v", id, ok, f)
	}
	noRepo := fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	id, ok, f = repoinit.Inspect(t.Context(), &fakeRepo{idErrs: []error{noRepo}}, target)
	if id != "" || ok || f != nil {
		t.Fatalf("not initialised: %q %v %v", id, ok, f)
	}
}

func TestFromResticMapsEachAnswerToItsReasonAndClass(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason repoinit.Reason
		class  repoinit.Class
	}{
		{"wrong password", fmt.Errorf("x: %w", restic.ErrWrongPassword), repoinit.WrongPassword, repoinit.ClassUsage},
		{"empty password", exitErr("an empty password is not allowed", restic.ErrEmptyPassword), repoinit.PasswordFileEmpty, repoinit.ClassUsage},
		{"network", exitErr("dial tcp: connection refused", restic.ErrNetwork), repoinit.BackendUnavailable, repoinit.ClassTemporary},
		{"bad output", fmt.Errorf("x: %w", restic.ErrBadOutput), repoinit.ResticOutputUnexpected, repoinit.ClassAgentError},
		{"anything else", exitErr("Access Denied", nil), repoinit.BackendRefused, repoinit.ClassAgentError},
		{"locked", fmt.Errorf("x: %w", restic.ErrLocked), repoinit.BackendRefused, repoinit.ClassAgentError},
	}
	for _, c := range cases {
		f := repoinit.FromRestic(t.Context(), c.err, target)
		if f.Reason != c.reason || f.Class != c.class {
			t.Errorf("%s: %+v", c.name, f)
		}
	}
}

func TestBackendMessagesNameTheTypeAndScrubTheCause(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), exitErr("Fatal: key SECRET rejected", nil), target)
	if want := "BACKEND_REFUSED: backend s3: key [x] rejected"; f.Error() != want {
		t.Errorf("refused: %q", f.Error())
	}
	f = repoinit.FromRestic(t.Context(), exitErr("SECRET: connection refused", restic.ErrNetwork), target)
	if want := "BACKEND_UNAVAILABLE: backend s3: [x]: connection refused; the command can be repeated"; f.Error() != want {
		t.Errorf("unavailable: %q", f.Error())
	}
	f = repoinit.FromRestic(t.Context(), fmt.Errorf("plain: %w", restic.ErrLocked), target)
	if !strings.Contains(f.Error(), "backend s3: plain: repository is locked") {
		t.Errorf("no restic message: %q", f.Error())
	}
}

func TestAWrongPasswordNamesThePasswordFileNotTheCause(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), fmt.Errorf("x: %w", restic.ErrWrongPassword), target)
	want := `WRONG_PASSWORD: a repository already exists at the address of "main", and the password file /etc/sard/main.pass does not open it`
	if f.Error() != want {
		t.Fatalf("%q", f.Error())
	}
}

func TestACancelledContextIsATimeoutOrAnInterruptNamingTheBackend(t *testing.T) {
	timedOut, cancel := context.WithCancelCause(t.Context())
	cancel(repoinit.ErrTimeout)
	f := repoinit.FromRestic(timedOut, errors.New("ignored"), target)
	if f.Reason != repoinit.Timeout || f.Class != repoinit.ClassTemporary || !strings.Contains(f.Detail, "backend s3") {
		t.Errorf("timeout: %+v", f)
	}
	interrupted, cancel2 := context.WithCancel(t.Context())
	cancel2()
	f = repoinit.FromRestic(interrupted, errors.New("ignored"), target)
	if f.Reason != repoinit.Interrupted || !strings.Contains(f.Detail, "backend s3") {
		t.Errorf("interrupt: %+v", f)
	}
	if repoinit.Interruption(t.Context()) != nil || repoinit.Interruption(timedOut).Reason != repoinit.Timeout {
		t.Error("Interruption disagrees with FromRestic")
	}
}

func TestCreateInitialisesAnAbsentRepository(t *testing.T) {
	noRepo := fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	r := &fakeRepo{idErrs: []error{noRepo}, initID: "new"}
	id, f := repoinit.Create(t.Context(), r, target)
	if id != "new" || f != nil || r.initRuns != 1 {
		t.Fatalf("%q %v %d", id, f, r.initRuns)
	}
}

func TestCreateNeverInitialisesAnExistingRepository(t *testing.T) {
	r := &fakeRepo{idErrs: []error{nil}, id: "abc"}
	id, f := repoinit.Create(t.Context(), r, target)
	if id != "" || f.Reason != repoinit.RepositoryExists || f.Class != repoinit.ClassExists || f.ID != "abc" || !strings.Contains(f.Detail, "abc") || r.initRuns != 0 {
		t.Fatalf("%q %+v %d", id, f, r.initRuns)
	}
}

func TestCreateReportsARepositoryThatAppearedBeforeInit(t *testing.T) {
	noRepo := fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	exists := fmt.Errorf("restic init: %w", restic.ErrRepositoryExists)
	r := &fakeRepo{idErrs: []error{noRepo, nil}, id: "abc", initErr: exists}
	_, f := repoinit.Create(t.Context(), r, target)
	if f.Reason != repoinit.RepositoryExists || f.ID != "abc" {
		t.Fatalf("%+v", f)
	}
	r = &fakeRepo{idErrs: []error{noRepo, noRepo}, initErr: exists}
	_, f = repoinit.Create(t.Context(), r, target)
	if f.Reason != repoinit.RepositoryExists || f.ID != "" || strings.Contains(f.Detail, "repository_id") {
		t.Fatalf("without an id: %+v", f)
	}
}

func TestCreateExplainsAnInitFailure(t *testing.T) {
	noRepo := fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	r := &fakeRepo{idErrs: []error{noRepo}, initErr: exitErr("Access Denied", nil)}
	_, f := repoinit.Create(t.Context(), r, target)
	if f.Reason != repoinit.BackendRefused || strings.Contains(f.Detail, "partially") {
		t.Fatalf("%+v", f)
	}
}

func TestAStoppedInitMayHaveCreatedTheRepositoryPartially(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	r := &cancellingRepo{cancel: func() { cancel(repoinit.ErrTimeout) }}
	_, f := repoinit.Create(ctx, r, target)
	if f.Reason != repoinit.Timeout || !strings.Contains(f.Detail, "may have been created partially") || !strings.Contains(f.Detail, "run the command again") {
		t.Fatalf("%+v", f)
	}
}

// cancellingRepo finds no repository, then its init is cut off.
type cancellingRepo struct {
	cancel func()
	restic.Repository
}

func (c *cancellingRepo) ID(context.Context) (string, error) {
	return "", fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
}

func (c *cancellingRepo) Init(context.Context) (string, error) {
	c.cancel()
	return "", errors.New("restic init: killed")
}

func TestScrubberMasksEnvValuesTheURLPasswordAndExtraValues(t *testing.T) {
	scrub := repoinit.Scrubber("rest:http://qa:URL-MARKER@127.0.0.1:9/x", []string{"A=ENV-MARKER", "B=", "C"}, "GENERATED", "")
	in := "a URL-MARKER b ENV-MARKER c GENERATED d qa 127.0.0.1"
	if got := scrub(in); got != "a [REDACTED] b [REDACTED] c [REDACTED] d qa 127.0.0.1" {
		t.Fatalf("%q", got)
	}
}

func TestScrubberIgnoresAddressesWithoutAPassword(t *testing.T) {
	for _, url := range []string{"/srv/backup/main", "s3:https://s3.example.com/b", "sftp:backup@nas.example.com:/main", "rest:http://qa@host/x", "rest:%zz"} {
		if got := repoinit.Scrubber(url, nil)("text " + url); got != "text "+url {
			t.Errorf("%s: %q", url, got)
		}
	}
}
