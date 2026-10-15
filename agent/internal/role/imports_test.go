// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package role_test

import (
	"os/exec"
	"strings"
	"testing"
)

// role is a leaf: of this repository it depends on itself and on the
// generated protobuf code.
func TestRoleDependsOnNothingOfTheAgent(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		_, rest, ok := strings.Cut(pkg, "github.com/Artur-Abalov/sard/")
		if !ok {
			continue
		}
		if rest != "agent/internal/role" && !strings.HasPrefix(rest, "proto/gen/go/") {
			t.Errorf("role depends on %s", pkg)
		}
	}
}
