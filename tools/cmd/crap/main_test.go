// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"fmt"
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

const report = `<report name="r"><package name="p"><class name="p/C">
<method name="ok" desc="()V"><counter type="INSTRUCTION" missed="0" covered="4"/><counter type="COMPLEXITY" missed="0" covered="2"/></method>
<method name="bad" desc="()V"><counter type="INSTRUCTION" missed="4" covered="0"/><counter type="COMPLEXITY" missed="5" covered="0"/></method>
</class></package></report>`

// manyMethods returns a report with one offender and n clean methods.
func manyMethods(n int) string {
	var b strings.Builder
	b.WriteString(`<report><package name="p"><class name="p/C">`)
	b.WriteString(`<method name="bad" desc="()V"><counter type="COMPLEXITY" missed="7" covered="0"/></method>`)
	for i := range n {
		fmt.Fprintf(&b, `<method name="m%02d" desc="()V"><counter type="COMPLEXITY" missed="0" covered="1"/></method>`, i)
	}
	b.WriteString(`</class></package></report>`)
	return b.String()
}

func runCmd(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodesAreStable(t *testing.T) {
	if exitOK != 0 || exitError != 1 || exitExceeded != 2 {
		t.Fatalf("exit codes changed: %d %d %d", exitOK, exitError, exitExceeded)
	}
}

func TestJaCoCoThresholdExceeded(t *testing.T) {
	xml := write(t, t.TempDir(), "r.xml", report)
	code, out, errOut := runCmd("-jacoco", xml)
	if code != 2 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "30.0") || !strings.Contains(errOut, "crap: 1 function(s) above threshold 6.0") {
		t.Fatalf("out = %s\nstderr = %s", out, errOut)
	}
}

func TestJaCoCoWithinRaisedThreshold(t *testing.T) {
	xml := write(t, t.TempDir(), "r.xml", report)
	code, out, _ := runCmd("-jacoco", xml, "-threshold", "30", "-top", "1")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(out, "p.C.ok") {
		t.Fatalf("-top 1 printed more than one row:\n%s", out)
	}
}

func TestDefaultTopIsOffendersPlusTenWorst(t *testing.T) {
	xml := write(t, t.TempDir(), "r.xml", manyMethods(15))
	for _, args := range [][]string{{"-jacoco", xml}, {"-jacoco", xml, "-top", "0"}} {
		_, out, _ := runCmd(args...)
		lines := strings.Count(out, "\n") - 1 // minus header
		if lines != 11 {
			t.Errorf("run(%q) printed %d rows, want 11:\n%s", args, lines, out)
		}
	}
}

func TestGoInput(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/m\n")
	write(t, dir, "a.go", "package m\n\nfunc A() {\n\tprintln()\n}\n")
	prof := write(t, dir, "cover.out", "mode: set\nexample.com/m/a.go:3.10,5.2 1 1\n")
	code, out, errOut := runCmd("-go-profile", prof, "-go-src", dir)
	if code != 0 || !strings.Contains(out, "a.go:A") {
		t.Fatalf("code = %d\nout = %s\nstderr = %s", code, out, errOut)
	}
}

func TestInputErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/m\n")
	write(t, dir, "bad.go", "package")
	goodProf := write(t, dir, "cover.out", "mode: set\n")
	badProf := write(t, dir, "bad.out", "nope\n")
	cases := []struct {
		args []string
		want string
	}{
		{nil, "crap: no input"},
		{[]string{"-unknown"}, "crap: flag provided but not defined: -unknown"},
		{[]string{"-jacoco", "x", "-go-profile", "y"}, "crap: use either"},
		{[]string{"-go-profile", "y"}, "crap: -go-profile and -go-src must be given together"},
		{[]string{"-jacoco", filepath.Join(dir, "missing.xml")}, "no such file"},
		{[]string{"-go-profile", goodProf, "-go-src", t.TempDir()}, "go.mod: no such file"},
		{[]string{"-go-profile", filepath.Join(dir, "missing"), "-go-src", dir}, "missing: no such file"},
		{[]string{"-go-profile", badProf, "-go-src", dir}, "missing mode line"},
		{[]string{"-go-profile", goodProf, "-go-src", dir}, "bad.go"},
	}
	for _, c := range cases {
		code, _, errOut := runCmd(c.args...)
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("run(%q) = %d, stderr %q; want 1 and %q", c.args, code, errOut, c.want)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestOutputError(t *testing.T) {
	xml := write(t, t.TempDir(), "r.xml", report)
	var errOut bytes.Buffer
	if code := run([]string{"-jacoco", xml}, brokenWriter{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "crap: file already closed") {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
}

// TestMainProcess runs main in a child process to check that it passes
// the real arguments through and exits with run's code.
func TestMainProcess(t *testing.T) {
	if xml := os.Getenv("CRAP_TEST_MAIN"); xml != "" {
		os.Args = []string{"crap", "-jacoco", xml}
		main()
		return
	}
	xml := write(t, t.TempDir(), "r.xml", report)
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(os.Environ(), "CRAP_TEST_MAIN="+xml)
	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 || !strings.Contains(string(out), "p.C.bad") {
		t.Fatalf("err = %v, output:\n%s", err, out)
	}
}
