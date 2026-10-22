// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/files"
)

// Scenario: Плагин files только планирует восстановление. The host writes the
// tree (ADR 0066 п. 1): the plugin has no restore of its own, no recovery of
// its journal entries and no extraction.
func TestTheFilesPluginOnlyPlansTheRestore(t *testing.T) {
	methods := reflect.TypeFor[files.Plugin]()
	for _, name := range []string{"Restore", "Recover", "PlanExtract"} {
		if _, ok := methods.MethodByName(name); ok {
			t.Errorf("the files plugin has %s", name)
		}
	}
	for _, name := range []string{"RestoreConfigSchema", "PlanRestore"} {
		if _, ok := methods.MethodByName(name); !ok {
			t.Errorf("the files plugin has no %s", name)
		}
	}
}

func TestThePackageOfTheFilesPluginImportsOnlyTheSDKOfTheAgent(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range strings.Fields(string(out)) {
		if strings.HasPrefix(imp, "github.com/Artur-Abalov/sard/") && imp != "github.com/Artur-Abalov/sard/agent/plugins/sdk" {
			t.Errorf("the files plugin imports %s", imp)
		}
	}
}
