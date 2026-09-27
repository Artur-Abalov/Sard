// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package commands_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/cli/internal/commands"
)

func execute(args ...string) (string, string, error) {
	var out, errOut bytes.Buffer
	root := commands.NewRoot("1.2.3", &out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func TestVersionPrintsTheInjectedVersion(t *testing.T) {
	out, errOut, err := execute("version")
	if err != nil || out != "sardctl 1.2.3\n" || errOut != "" {
		t.Fatalf("out = %q, stderr = %q, err = %v", out, errOut, err)
	}
}

func TestNoArgumentsPrintsHelp(t *testing.T) {
	out, _, err := execute()
	if err != nil || !strings.HasPrefix(out, "sardctl manages a Sard backup orchestrator from the command line\n") ||
		!strings.Contains(out, "Available Commands:") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestErrorsAreReturnedNotPrinted(t *testing.T) {
	out, errOut, err := execute("restore")
	if err == nil || out != "" || errOut != "" {
		t.Fatalf("out = %q, stderr = %q, err = %v", out, errOut, err)
	}
}

func TestVersionTakesNoArguments(t *testing.T) {
	if _, _, err := execute("version", "x"); err == nil {
		t.Fatal("version accepted an argument")
	}
}
