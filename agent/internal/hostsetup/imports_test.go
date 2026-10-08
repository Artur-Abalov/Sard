// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"os/exec"
	"strings"
	"testing"
)

// hostsetup is a leaf: of the packages of the agent it depends on its own,
// config and refusal, and so on nothing that runs restic.
func TestHostsetupDependsOnNothingOfTheAgentButConfigAndRefusal(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"hostsetup": true, "config": true, "refusal": true}
	for _, pkg := range strings.Fields(string(out)) {
		_, rest, ok := strings.Cut(pkg, "github.com/Artur-Abalov/sard/")
		if !ok {
			continue
		}
		if name, found := strings.CutPrefix(rest, "agent/internal/"); !found || !allowed[name] {
			t.Errorf("hostsetup depends on %s", pkg)
		}
	}
}
