// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Recoverer = Plugin{}

// Recover implements sdk.Recoverer: at the start of the agent it judges the
// entry of a restore of a database the previous run did not finish (ВК14,
// ВК27). A restore that completed stays (Restored); one that had created
// something is rolled back by the OIDs of the entry (NothingRestored); what
// cannot be dropped, or was only the intent to create it and is never dropped
// by name, is named (LeftBehind). An entry that holds an OID to drop asks to be
// kept, so that the next start tries again; one of intents only goes with the
// result.
func (p Plugin) Recover(ctx context.Context, h sdk.RecoveryHost, commandID string, data []byte) sdk.Recovery {
	entry, err := parseEntry(data)
	if err != nil {
		return sdk.LeftBehind(nil, err.Error(), true)
	}
	if r := entry.Result; r != nil {
		return sdk.Restored(sdk.RestoreResult{Target: r.Database, Database: r.Database, Tables: r.Tables, Roles: r.Roles})
	}
	left := p.rollBackEntry(ctx, h, entry)
	if len(left.leftover) == 0 {
		return sdk.NothingRestored()
	}
	if !left.rest.sameObjects(entry) {
		if err := h.Journal().Save(left.rest.bytes()); err != nil {
			h.Log(sdk.LevelError, "cannot keep in the rollback journal what the rollback of the restore "+commandID+" left: "+err.Error())
		}
	}
	return sdk.LeftBehind(left.leftover, strings.Join(left.causes, "; "), left.rest.created())
}

// rollBackEntry drops what the entry records. A restore that could not even
// start (no password, no psql) leaves everything the entry records.
func (p Plugin) rollBackEntry(ctx context.Context, h sdk.RecoveryHost, e journalEntry) undone {
	c := conn{p: p, log: h, to: e.Connection} // an object without an OID is only named: no connection needed
	if e.created() {
		var err error
		if c, err = p.connectionOf(h, e); err != nil {
			return keepAll(e, err)
		}
	}
	return undo(ctx, c, e)
}

// keepAll leaves everything the entry records, for the reason.
func keepAll(e journalEntry, why error) undone {
	u := undone{rest: e}
	if e.Database.Name != "" {
		u.keep("database "+e.Database.Name, why.Error())
	}
	for _, r := range e.Roles {
		u.keep("role "+r.Name, why.Error())
	}
	return u
}

// connectionOf rebuilds the connection of an interrupted restore from its
// entry: the parameters from the entry, the password from the secret of the
// host the entry names, the programs from the host.
func (p Plugin) connectionOf(h sdk.RecoveryHost, e journalEntry) (conn, error) {
	to, password, err := e.Connection.secured(h, e.Database.Name)
	if err != nil {
		return conn{}, errors.New("the secret " + e.Connection.PasswordRef + " of the interrupted restore cannot be used: " + err.Error())
	}
	t, f := p.locateRestore(e.Connection.ClientPath)
	if f != nil {
		return conn{}, errors.New(f.Reason + ": " + f.Detail)
	}
	return conn{p: p, log: h, to: to, password: password, tools: t}, nil
}
