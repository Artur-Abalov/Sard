// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The executor runs what it is given; where the role, the certificate or
// the configuration come from is not its business.
func TestExecutorDoesNotDependOnWhereTheRoleComesFrom(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		_, rest, ok := strings.Cut(pkg, "github.com/Artur-Abalov/sard/")
		if !ok {
			continue
		}
		for _, banned := range []string{"identity", "tlsid", "config", "transport"} {
			if rest == "agent/internal/"+banned {
				t.Errorf("executor depends on %s", pkg)
			}
		}
	}
}
