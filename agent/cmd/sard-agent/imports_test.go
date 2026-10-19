// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The test helpers stay out of the binary: nothing the agent links in is a
// test package or a package that exists for tests.
func TestTheBinaryLinksNoTestCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "testing" || strings.HasSuffix(pkg, "/certtest") || strings.HasSuffix(pkg, "/testplugin") {
			t.Errorf("the agent depends on %s", pkg)
		}
	}
}
