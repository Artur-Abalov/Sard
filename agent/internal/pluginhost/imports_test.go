// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"os/exec"
	"strings"
	"testing"
)

// pluginhost runs the steps of plugins; it does not know the commands of the
// host operator, nor their rules (F3).
func TestPluginhostDoesNotImportTheHostCommands(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"repoinit", "repoconnect", "hostsetup", "hostcheck", "enroll", "serverprobe"}
	for _, pkg := range strings.Fields(string(out)) {
		for _, name := range forbidden {
			if pkg == "github.com/Artur-Abalov/sard/agent/internal/"+name {
				t.Errorf("pluginhost imports internal/%s", name)
			}
		}
	}
}
