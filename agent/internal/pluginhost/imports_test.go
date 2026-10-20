// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
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

// Scenario: Тестовые плагины не входят в агент. The plugin of the tests is
// imported by tests only.
func TestNoCodeOfTheAgentImportsThePluginOfTheTests(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "github.com/Artur-Abalov/sard/agent/...").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		for _, imp := range fields[1:] {
			if strings.HasSuffix(imp, "/pluginhost/journaltest") {
				t.Errorf("%s imports %s", fields[0], imp)
			}
		}
	}
}

// Scenario: Общие коды причин SDK совпадают с кодами агента.
func TestTheGenericReasonsOfTheSDKAreThoseOfTheAgent(t *testing.T) {
	agent := map[string]refusal.Reason{
		sdk.ReasonBackendUnavailable: refusal.BackendUnavailable, sdk.ReasonBackendRefused: refusal.BackendRefused,
		sdk.ReasonStorageAccessDenied: refusal.StorageAccessDenied, sdk.ReasonWrongPassword: refusal.WrongPassword,
		sdk.ReasonPasswordFileMissing: refusal.PasswordFileMissing, sdk.ReasonRepositoryUninit: refusal.RepositoryNotInitialized,
		sdk.ReasonSnapshotUnknown: refusal.SnapshotUnknown, sdk.ReasonResticFailed: refusal.ResticFailed,
		sdk.ReasonResticOutputUnexpected: refusal.ResticOutputUnexpected, sdk.ReasonInterrupted: refusal.Interrupted,
		sdk.ReasonTimeout: refusal.Timeout, sdk.ReasonCleanupFailed: refusal.CleanupFailed,
	}
	for _, code := range sdk.GenericReasons() {
		want, ok := agent[code]
		if !ok || string(want) != code {
			t.Errorf("the SDK reason %q is not a reason of the agent with the same text", code)
		}
		if !sdk.ValidReason(code) {
			t.Errorf("the SDK reason %q does not fit the pattern", code)
		}
	}
	if len(agent) != len(sdk.GenericReasons()) {
		t.Errorf("the table names %d reasons, the SDK lists %d", len(agent), len(sdk.GenericReasons()))
	}
}
