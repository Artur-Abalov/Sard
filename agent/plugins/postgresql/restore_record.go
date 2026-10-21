// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"encoding/json"
	"fmt"
	"slices"
)

// journalObject is a database or a role the restore creates: its name is
// written before it is created (the intent), its OID once it exists. An object
// whose OID is not known is never dropped by name (ВК16).
type journalObject struct {
	Name string `json:"name"`
	OID  uint32 `json:"oid,omitempty"`
}

// journalResult is the outcome of a restore that finished.
type journalResult struct {
	Database string `json:"database"`
	Tables   uint64 `json:"tables_restored"`
	Roles    uint64 `json:"roles_restored"`
}

// journalEntry is the entry of the restore journal of one restore of a database
// (ADR 0066: the bytes the host keeps for the plugin; the file layout of the
// previous release is this entry and nothing around it). It passes the states of
// ВК27, each saved before the action it announces: the intent (an object with a
// name and no OID), created (with its OID) and completed (Result). It names the
// secret of the password and never holds its value.
type journalEntry struct {
	CommandID  string          `json:"command_id"`
	Connection target          `json:"connection"`
	Database   journalObject   `json:"database"`
	Roles      []journalObject `json:"roles,omitempty"`
	Result     *journalResult  `json:"result,omitempty"`
}

func newEntry(commandID string, to target) journalEntry {
	return journalEntry{CommandID: commandID, Connection: to}
}

// bytes is the entry as the host keeps it.
func (e journalEntry) bytes() []byte {
	data, _ := json.Marshal(e) // plain fields: Marshal cannot fail
	return data
}

// parseEntry reads the entry the host handed back at the start.
func parseEntry(data []byte) (journalEntry, error) {
	var e journalEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return journalEntry{}, fmt.Errorf("the rollback journal entry is unreadable: %w", err)
	}
	return e, nil
}

// created reports whether some object of the entry has an OID: the restore
// went as far as creating it.
func (e journalEntry) created() bool {
	return e.Database.OID != 0 || slices.ContainsFunc(e.Roles, func(o journalObject) bool { return o.OID != 0 })
}

// sameObjects: both entries name the same database and the same roles.
func (e journalEntry) sameObjects(other journalEntry) bool {
	return e.Database == other.Database && slices.Equal(e.Roles, other.Roles)
}
