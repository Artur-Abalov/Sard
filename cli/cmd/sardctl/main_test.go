// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runCtl(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	code, out, errOut := runCtl("version")
	if code != 0 || out != "sardctl dev\n" || errOut != "" {
		t.Fatalf("code = %d, out = %q, stderr = %q", code, out, errOut)
	}
}

func TestHelpListsCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		code, out, _ := runCtl(args...)
		if code != 0 || !strings.Contains(out, "Available Commands:") || !strings.Contains(out, "version     Print the sardctl version") {
			t.Errorf("run(%q) = %d, out = %q", args, code, out)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	code, out, errOut := runCtl("backup")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, `sardctl: unknown command "backup"`) {
		t.Fatalf("code = %d, out = %q, stderr = %q", code, out, errOut)
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	if code, _, errOut := runCtl("version", "extra"); code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestVersionWriteFailure(t *testing.T) {
	var errOut bytes.Buffer
	if code := run([]string{"version"}, failingWriter{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "file already closed") {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
}

func TestMainProcess(t *testing.T) {
	if os.Getenv("SARDCTL_TEST_MAIN") == "1" {
		os.Args = []string{"sardctl", "version"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(os.Environ(), "SARDCTL_TEST_MAIN=1")
	out, err := cmd.Output()
	if err != nil || !strings.HasPrefix(string(out), "sardctl dev\n") {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}
