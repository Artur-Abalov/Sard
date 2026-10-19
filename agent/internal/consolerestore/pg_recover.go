// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// CleanDatabases goes through the rollback journal before any step runs
// (ВК14, ВК27) and settles every entry: a restore that completed stays
// (Completed) until Settle; one that had created something is rolled back by
// the OIDs of the journal (NothingRestored); what cannot be dropped, or was
// only the intent to create it and is never dropped by name, is named
// (CleanupFailed). It logs each object it drops and each it cannot (as an
// error).
func (e *Engine) CleanDatabases() []Evidence {
	if e.opts.Postgres.StateDir == "" {
		return nil
	}
	j, err := e.openJournal(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	path := filepath.Join(e.opts.Postgres.StateDir, journalDirName)
	if err != nil {
		return e.unusableJournal(path, err)
	}
	defer j.close()
	return e.settleAll(j, path)
}

func (e *Engine) unusableJournal(path string, err error) []Evidence {
	e.opts.Logger.Error("cannot read the rollback journal of restores of databases", "path", path, "error", err)
	return []Evidence{{Path: path, Verdict: CleanupFailed, Err: err}}
}

func (e *Engine) settleAll(j *pgJournal, path string) []Evidence {
	ids, err := j.commands()
	if err != nil {
		return e.unusableJournal(path, err)
	}
	var found []Evidence
	for _, id := range ids {
		found = append(found, e.settleRecord(j, id, filepath.Join(path, fileOf(id))))
	}
	return found
}

// settleRecord judges one entry of the journal.
func (e *Engine) settleRecord(j *pgJournal, id, path string) Evidence {
	rec, err := j.load(id)
	if err != nil {
		e.opts.Logger.Error("cannot read an entry of the rollback journal; judge the restore by hand", "path", path, "error", err)
		return Evidence{Name: id, Path: path, Verdict: CleanupFailed, Err: err}
	}
	if rec.Result != nil {
		e.opts.Logger.Info("the restore of a database completed before the agent was restarted", "path", path, "database", rec.Result.Database)
		return Evidence{Name: id, Path: path, Verdict: Completed, Database: &DatabaseOutput{Database: rec.Result.Database, Tables: rec.Result.Tables, Roles: rec.Result.Roles}}
	}
	left := e.rollBackRecord(rec)
	if len(left.leftover) > 0 {
		return e.notRolledBack(j, id, path, left)
	}
	e.removeEntry(j, id)
	e.opts.Logger.Info("rolled back an interrupted restore of a database", "command_id", id)
	return Evidence{Name: id, Path: path, Verdict: NothingRestored}
}

// notRolledBack keeps in the entry what is left, and names it.
func (e *Engine) notRolledBack(j *pgJournal, id, path string, left undone) Evidence {
	_ = j.save(left.rest)
	err := errors.New(strings.Join(left.causes, "; "))
	e.opts.Logger.Error("the rollback of an interrupted restore of a database could not drop what it created; remove it by hand",
		"command_id", id, "left", strings.Join(left.leftover, ", "), "error", err)
	return Evidence{Name: id, Path: path, Verdict: CleanupFailed, Err: err, Leftover: strings.Join(left.leftover, ", ")}
}

// rollBackRecord drops what the entry records. A restore that could not
// even start (no password, no psql) leaves everything the entry records.
func (e *Engine) rollBackRecord(rec pgRecord) undone {
	var conn pgConn // an object without an OID is only named: no connection needed
	if rec.created() {
		var err error
		if conn, err = e.connectionOf(rec); err != nil {
			return keepAll(rec, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	return e.undo(ctx, conn, logReporter{e.opts.Logger}, rec)
}

// keepAll leaves everything the entry records, for the reason.
func keepAll(rec pgRecord, why error) undone {
	u := undone{rest: rec}
	if rec.Database.Name != "" {
		u.keep("database "+rec.Database.Name, why.Error())
	}
	for _, r := range rec.Roles {
		u.keep("role "+r.Name, why.Error())
	}
	return u
}

// connectionOf rebuilds the connection of an interrupted restore from its
// entry: the parameters from the journal, the password from the secret of the
// host the entry names, the programs from the host.
func (e *Engine) connectionOf(rec pgRecord) (pgConn, error) {
	if e.opts.Postgres.Secret == nil {
		return pgConn{}, errors.New("the host has no secrets to read the password of the interrupted restore from")
	}
	password, err := e.opts.Postgres.Secret(rec.Connection.PasswordRef)
	if err != nil {
		return pgConn{}, errors.New("cannot read the secret " + rec.Connection.PasswordRef + " of the interrupted restore: " + err.Error())
	}
	password = []byte(strings.TrimRight(string(password), "\r\n"))
	tools, f := e.locate(rec.Connection.ClientPath, e.environ()())
	if f != nil {
		return pgConn{}, errors.New(string(refusal.PGClientMissing) + ": " + f.Detail)
	}
	return pgConn{plan: rec.plan(), password: password, tools: tools, environ: e.environ(), exec: e.opts.Postgres.Exec}, nil
}
