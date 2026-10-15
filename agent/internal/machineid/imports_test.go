// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package machineid_test

import (
	"os/exec"
	"strings"
	"testing"
)

// machineid is a leaf: of this repository it depends on itself only.
func TestMachineidDependsOnNothingOfTheAgent(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		_, rest, ok := strings.Cut(pkg, "github.com/Artur-Abalov/sard/")
		if ok && rest != "agent/internal/machineid" {
			t.Errorf("machineid depends on %s", pkg)
		}
	}
}
