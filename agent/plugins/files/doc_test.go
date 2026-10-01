// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario: Документация плагина объясняет, от чьего имени читаются файлы.
func TestTheDocumentationSaysWhoReadsTheFilesAndHowToGrantRead(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "plugins", "files.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	for _, want := range []string{
		"sard-agent",           // the user the agent runs as
		"не root",              // and that it is not root
		"usermod -aG",          // read through a group
		"setfacl",              // read through an ACL
		"setfacl -R -d",        // with a default ACL for new files
		"CAP_DAC_READ_SEARCH",  // read through a capability
		"AmbientCapabilities=", // of the systemd unit
		"чтение всех файлов хоста", // and what it costs
		"PrivateTmp", // the unit's own /tmp
		"https://restic.readthedocs.io/en/stable/040_backup.html#excluding-files",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/plugins/files.md does not contain %q", want)
		}
	}
}
