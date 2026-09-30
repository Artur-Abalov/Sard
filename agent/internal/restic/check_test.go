// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func checkErr(t *testing.T, r reply) *restic.CheckError {
	t.Helper()
	f := newFixture(t, map[string]reply{"version": r})
	err := f.build().Check(context.Background())
	var ce *restic.CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("Check = %v, want a *CheckError", err)
	}
	return ce
}

func TestCheckAcceptsTheMinimumAndNewer(t *testing.T) {
	for _, v := range []string{"0.19.0", "0.19.1", "0.19.0-dev", "0.20.0", "1.0.0"} {
		f := newFixture(t, map[string]reply{"version": versionReply("restic " + v + " compiled with go1.26.4 on linux/amd64\n")})
		if err := f.build().Check(context.Background()); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
}

func TestCheckNamesTheFoundVersionAsResticPrintedIt(t *testing.T) {
	for _, v := range []string{"0.18.1", "0.18.1-dev", "0.17.3", "0.9.6"} {
		ce := checkErr(t, versionReply("restic "+v+" compiled with go1.22.1 on linux/amd64\n"))
		if ce.Problem != restic.TooOld || ce.Found != v || ce.Minimum != restic.Minimum || ce.Binary != "/opt/sard/restic" {
			t.Errorf("%s: %+v", v, ce)
		}
		if !errors.Is(ce, restic.ErrUnsupportedVersion) {
			t.Errorf("%s: %v does not wrap ErrUnsupportedVersion", v, ce)
		}
	}
}

func TestCheckReportsAMissingBinary(t *testing.T) {
	ce := checkErr(t, reply{code: -1, err: fs.ErrNotExist})
	if ce.Problem != restic.NotFound || ce.Binary != "/opt/sard/restic" || !errors.Is(ce, fs.ErrNotExist) {
		t.Fatalf("%+v", ce)
	}
}

func TestCheckReportsAnUnusableBinary(t *testing.T) {
	for name, r := range map[string]reply{
		"not executable": {code: -1, err: fs.ErrPermission},
		"prints hello":   versionReply("hello\n"),
		"exits with 1":   {code: 1},
		"prints nothing": {},
	} {
		if ce := checkErr(t, r); ce.Problem != restic.Unusable || ce.Err == nil || ce.Binary != "/opt/sard/restic" {
			t.Errorf("%s: %+v", name, ce)
		}
	}
}

func TestCheckLetsACancelledContextThrough(t *testing.T) {
	f := newFixture(t, map[string]reply{"version": {code: -1}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.build().Check(ctx)
	var ce *restic.CheckError
	if !errors.Is(err, context.Canceled) || errors.As(err, &ce) {
		t.Fatalf("Check = %v", err)
	}
}

func TestParseEnvFileReturnsTheAssignmentsAndNamesTheBadLine(t *testing.T) {
	env, err := restic.ParseEnvFile([]byte("# c\nAWS_ACCESS_KEY_ID=key\n\nAWS_SECRET_ACCESS_KEY=ENV-MARKER\n"))
	if err != nil || len(env) != 2 || env[1] != "AWS_SECRET_ACCESS_KEY=ENV-MARKER" {
		t.Fatalf("env = %q, err = %v", env, err)
	}
	for _, bad := range []string{"ENV-MARKER no equals sign", "RESTIC_PASSWORD=ENV-MARKER", "LD_PRELOAD=ENV-MARKER"} {
		_, err := restic.ParseEnvFile([]byte("A=b\n" + bad + "\n"))
		if !errors.Is(err, restic.ErrInvalidEnvFile) || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "ENV-MARKER") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestCheckErrorTextAndUnwrapPerProblem(t *testing.T) {
	cause := errors.New("boom")
	cases := []struct {
		name string
		ce   *restic.CheckError
		text string
		is   error
	}{
		{"too old", &restic.CheckError{Problem: restic.TooOld, Binary: "/b/restic", Found: "0.18.1", Minimum: restic.Minimum},
			"restic 0.18.1 at /b/restic is older than the minimum " + restic.Minimum.String(), restic.ErrUnsupportedVersion},
		{"not found", &restic.CheckError{Problem: restic.NotFound, Binary: "/b/restic", Err: fs.ErrNotExist},
			"restic at /b/restic is not found: file does not exist", fs.ErrNotExist},
		{"unusable", &restic.CheckError{Problem: restic.Unusable, Binary: "/b/restic", Err: cause},
			"restic at /b/restic is unusable: boom", cause},
	}
	for _, c := range cases {
		if got := c.ce.Error(); got != c.text {
			t.Errorf("%s: Error() = %q, want %q", c.name, got, c.text)
		}
		if !errors.Is(c.ce, c.is) {
			t.Errorf("%s: does not wrap %v", c.name, c.is)
		}
	}
	if errors.Is(cases[0].ce, cause) || cases[0].ce.Unwrap() != restic.ErrUnsupportedVersion {
		t.Error("TooOld must unwrap to ErrUnsupportedVersion only")
	}
}
