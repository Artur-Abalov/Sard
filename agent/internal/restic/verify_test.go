// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The golden outputs of this file are restic 0.19.1's, recorded with a key
// and storage that may only read (V5, phase 1 of the verifier).

func TestVerifyDataChecksAPercentOfThePacksWithoutLockOrCache(t *testing.T) {
	f := newFixture(t, map[string]reply{"check": {stdout: "check-ok.stdout"}})

	report, err := f.build().VerifyData(context.Background(), 10)

	if err != nil || len(report.Errors) != 0 {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	want := []string{"check", "--json", "--read-data-subset=10%", "--no-lock", "--no-cache"}
	if got := f.exec.call("check").Args; !slices.Equal(got, want) {
		t.Errorf("args %q", got)
	}
}

func TestVerifyDataReportsADamagedRepositoryWithWhatResticFound(t *testing.T) {
	f := newFixture(t, map[string]reply{"check": {stdout: "check-damaged.stdout", stderr: "check-damaged.stderr", code: 1}})

	report, err := f.build().VerifyData(context.Background(), 10)

	// restic's error lines name no file: the message lists none.
	if !errors.Is(err, restic.ErrDamaged) || err.Error() != "repository contains errors: restic check: exit code 1: Fatal: repository contains errors" {
		t.Fatalf("err = %v", err)
	}
	if len(report.Errors) != 2 || report.Errors[0] != "pack 7054adb01ef83e321ef24fec17a22eb2e474b2ca723a388eb1f8e352ad21c4b4: does not exist" {
		t.Fatalf("errors = %q", report.Errors)
	}
}

// restic says num_errors 1 in its summary also when it could not open the
// repository: only the fatal message tells a damaged one.
func TestVerifyDataDoesNotCallARepositoryItCouldNotOpenDamaged(t *testing.T) {
	f := newFixture(t, map[string]reply{"check": {stdout: "check-unopened.stdout", stderr: "check-unopened.stderr", code: 1}})

	_, err := f.build().VerifyData(context.Background(), 10)

	if err == nil || errors.Is(err, restic.ErrDamaged) {
		t.Fatalf("err = %v", err)
	}
	var exit *restic.ExitError
	if !errors.As(err, &exit) || exit.Cause() != `unable to open repository at sftp:nohost:/x: exec: "ssh": executable file not found in $PATH` {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyDataReportsTheExitCodesOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"check": {code: 12}})

	if _, err := f.build().VerifyData(context.Background(), 10); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreVerifiedRestoresAndReadsBackWithoutLockOrCache(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {stdout: "restore.stdout"}})

	if err := f.build().RestoreVerified(context.Background(), "6c719cbc", "/var/lib/sard-agent/verify/j1/copy"); err != nil {
		t.Fatal(err)
	}
	want := []string{"restore", "--json", "--verify", "--target", "/var/lib/sard-agent/verify/j1/copy", "--no-lock", "--no-cache", "--", "6c719cbc"}
	if got := f.exec.call("restore").Args; !slices.Equal(got, want) {
		t.Errorf("args %q", got)
	}
}

func TestRestoreVerifiedRejectsInvalidRequestsAndReportsFailures(t *testing.T) {
	for _, c := range [][2]string{{"", "/t"}, {"6c719cbc", ""}} {
		f := newFixture(t, nil)
		if err := f.build().RestoreVerified(context.Background(), c[0], c[1]); !errors.Is(err, restic.ErrInvalidRequest) || len(f.exec.calls) != 0 {
			t.Errorf("%q: err = %v", c, err)
		}
	}
	f := newFixture(t, map[string]reply{"restore": {stderr: "restore-missing.stderr", code: 1}})
	if err := f.build().RestoreVerified(context.Background(), "deadbeef", "/t"); err == nil {
		t.Fatal("a failed restore passed")
	}
}
