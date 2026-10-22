// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// keyWarningText is the line of stderr for a warning of the key list (K13).
func keyWarningText(code, repository string) string {
	if code == restickeys.WarnSingleKey {
		return "SINGLE_KEY: only one key: losing this host loses the backups"
	}
	return fmt.Sprintf("NO_RECOVERY_KEY: no recovery key: sudo sard-agent repo key add %s --for recovery", repository)
}

// keyJSON is one key of "repo key list --json" (K13): purpose and label are
// null for a key that Sard did not issue.
type keyJSON struct {
	ID      string  `json:"id"`
	Purpose *string `json:"purpose"`
	Label   *string `json:"label"`
	Host    string  `json:"host"`
	Created string  `json:"created"`
	Current bool    `json:"current"`
}

// printKeysJSON is {"repository","keys","warnings"} on one line.
func printKeysJSON(stdout io.Writer, repository string, keys []restic.Key, warnings []string) {
	out := struct {
		Repository string    `json:"repository"`
		Keys       []keyJSON `json:"keys"`
		Warnings   []string  `json:"warnings"`
	}{Repository: repository, Keys: []keyJSON{}, Warnings: append([]string{}, warnings...)}
	for _, k := range keys {
		purpose, label := keyTagColumns(k)
		out.Keys = append(out.Keys, keyJSON{ID: k.ID, Purpose: nullIfDash(purpose), Label: nullIfDash(label),
			Host: k.HostName, Created: k.Created.UTC().Format(time.RFC3339), Current: k.Current})
	}
	printJSON(stdout, out)
}
