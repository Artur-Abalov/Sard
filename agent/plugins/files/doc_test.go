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

// Scenario (Ф20): Документация плагина объясняет неполный бэкап и пометку
// полноты на хосте.
func TestTheDocumentationExplainsAnIncompleteBackupAndTheCompletenessMark(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "plugins", "files.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	for _, want := range []string{
		"## От чьего имени читаются файлы", // the section the server hint links to
		"до десяти первых путей",           // what the server shows
		"подсказку о правах",
		"sard-agent repo restore",     // the host restores an incomplete snapshot
		"колонке COMPLETE",            // the mark in repo snapshots
		"unconfirmed",                 // its value
		"до появления отпечатков",     // snapshots made before fingerprints
		"снимок отпечатка не создан",  // a failed fingerprint snapshot
		"completeness: not confirmed", // what repo restore says
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/plugins/files.md does not contain %q", want)
		}
	}
}
