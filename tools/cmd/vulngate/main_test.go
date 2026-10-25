// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	grypeBlocking = `{"matches":[{"vulnerability":{"id":"CVE-1","severity":"High","fix":{"state":"fixed","versions":["1.1"]}},"artifact":{"name":"lib","version":"1.0"}}]}`
	grypeClean    = `{"matches":[{"vulnerability":{"id":"CVE-2","severity":"Low","fix":{"state":"fixed","versions":["1.1"]}},"artifact":{"name":"lib","version":"1.0"}}]}`
	govulnCalled  = `{"config":{}}
{"finding":{"osv":"GO-1","trace":[{"module":"stdlib","version":"v1.27.1","function":"Serve"}]}}`
)

func runWith(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodesAreStable(t *testing.T) {
	if exitOK != 0 || exitError != 1 || exitBlocked != 2 {
		t.Fatal("exit codes changed: CI reads them")
	}
}

func TestPassesWithoutBlockingFindings(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "# none\n")
	code, out, errOut := runWith("-format", "grype", "-exceptions", ex, "-today", "2026-10-25", write(t, dir, "r", grypeClean))
	if code != exitOK || out != "" || errOut != "vulngate: 0 blocking, 0 excepted, 0 warnings\n" {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestFailsOnABlockingFinding(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "")
	clean := write(t, dir, "clean", grypeClean)
	bad := write(t, dir, "bad", grypeBlocking)
	code, out, errOut := runWith("-format", "grype", "-exceptions", ex, "-today", "2026-10-25", clean, bad)
	if code != exitBlocked || out != "BLOCKING CVE-1 lib@1.0 high, fixed in 1.1\n" ||
		errOut != "vulngate: 1 blocking, 0 excepted, 0 warnings\n" {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestAnExceptionLetsItPass(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "CVE-1 2026-11-30 the vulnerable parser never sees untrusted input\n")
	code, out, errOut := runWith("-format", "grype", "-exceptions", ex, "-today", "2026-10-25", write(t, dir, "r", grypeBlocking))
	if code != exitOK || out != "EXCEPTED CVE-1 lib@1.0 high, fixed in 1.1\n" ||
		errOut != "vulngate: 0 blocking, 1 excepted, 0 warnings\n" {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestAnExpiredExceptionFails(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "CVE-1 2026-10-01 was fine for a while\n")
	code, _, errOut := runWith("-format", "grype", "-exceptions", ex, "-today", "2026-10-25", write(t, dir, "r", grypeBlocking))
	if code != exitError || !strings.Contains(errOut, "line 1: CVE-1: expired on 2026-10-01") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

func TestGovulncheck(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "")
	code, out, _ := runWith("-format", "govulncheck", "-exceptions", ex, "-today", "2026-10-25", write(t, dir, "r", govulnCalled))
	if code != exitBlocked || out != "BLOCKING GO-1 stdlib@v1.27.1 called by our code, no fixed version\n" {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestTodayDefaultsToNow(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "CVE-1 2000-01-01 long gone\n")
	code, _, errOut := runWith("-format", "grype", "-exceptions", ex, write(t, dir, "r", grypeClean))
	if code != exitError || !strings.Contains(errOut, "expired") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "")
	r := write(t, dir, "r", grypeClean)
	cases := map[string]struct {
		args []string
		want string
	}{
		"no format":         {[]string{"-exceptions", ex, r}, `-format "": want grype or govulncheck`},
		"unknown format":    {[]string{"-format", "trivy", "-exceptions", ex, r}, `-format "trivy"`},
		"no exceptions":     {[]string{"-format", "grype", r}, "-exceptions is required"},
		"no report":         {[]string{"-format", "grype", "-exceptions", ex}, "no report given"},
		"bad today":         {[]string{"-format", "grype", "-exceptions", ex, "-today", "soon", r}, "-today:"},
		"bad flag":          {[]string{"-bogus"}, "bogus"},
		"missing report":    {[]string{"-format", "grype", "-exceptions", ex, filepath.Join(dir, "absent")}, "absent"},
		"missing exception": {[]string{"-format", "grype", "-exceptions", filepath.Join(dir, "absent"), r}, "absent"},
		"broken report":     {[]string{"-format", "grype", "-exceptions", ex, write(t, dir, "broken", "{")}, "broken: grype report"},
	}
	for name, c := range cases {
		if code, _, errOut := runWith(c.args...); code != exitError || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code %d, err %q, want %q", name, code, errOut, c.want)
		}
	}
}

func TestOutputError(t *testing.T) {
	dir := t.TempDir()
	ex := write(t, dir, "ex", "")
	var errOut bytes.Buffer
	args := []string{"-format", "grype", "-exceptions", ex, "-today", "2026-10-25", write(t, dir, "r", grypeBlocking)}
	code := run(args, failingWriter{}, &errOut)
	if code != exitError || !strings.Contains(errOut.String(), "file already closed") {
		t.Fatalf("code %d, err %q", code, errOut.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// TestMainProcess runs main in a child process to check that it passes
// the real arguments through and exits with run's code.
func TestMainProcess(t *testing.T) {
	if report := os.Getenv("VULNGATE_TEST_MAIN"); report != "" {
		os.Args = []string{"vulngate", "-format", "grype", "-exceptions", os.Getenv("VULNGATE_TEST_EX"), report}
		main()
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(os.Environ(), "VULNGATE_TEST_MAIN="+write(t, dir, "r", grypeBlocking), "VULNGATE_TEST_EX="+write(t, dir, "ex", ""))
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitBlocked || !strings.Contains(string(out), "BLOCKING CVE-1") {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
}
