// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// journalDirName is the directory of the rollback journal inside
// executor.state_dir (ВК27); journalSuffix ends the file of a command.
const (
	journalDirName = "pg-restore"
	journalSuffix  = ".json"
	journalTemp    = ".tmp"
)

// pgObject is a database or a role the restore creates: its name is written
// before it is created (the intent), its OID once it exists. An object whose
// OID is not known is never dropped by name (ВК16).
type pgObject struct {
	Name string `json:"name"`
	OID  uint32 `json:"oid,omitempty"`
}

// pgResult is the outcome of a restore that finished.
type pgResult struct {
	Database string `json:"database"`
	Tables   uint64 `json:"tables_restored"`
	Roles    uint64 `json:"roles_restored"`
}

// pgConnection is the target as the journal keeps it: the parameters and the
// name of the secret, never the password.
type pgConnection struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	PasswordRef string `json:"password_ref"`
	TLSMode     string `json:"tls_mode,omitempty"`
	TLSRootCert string `json:"tls_root_cert,omitempty"`
	ClientPath  string `json:"client_path,omitempty"`
}

// pgRecord is the journal entry of one restore of a database. It passes the
// states of ВК27, each written with fsync before the action it announces:
// the intent (an object with a name and no OID), created (with its OID) and
// completed (Result).
type pgRecord struct {
	CommandID  string       `json:"command_id"`
	Connection pgConnection `json:"connection"`
	Database   pgObject     `json:"database"`
	Roles      []pgObject   `json:"roles,omitempty"`
	Result     *pgResult    `json:"result,omitempty"`
}

func recordOf(commandID string, plan sdk.PostgresRestore) pgRecord {
	return pgRecord{CommandID: commandID, Connection: pgConnection{
		Host: plan.Host, Port: plan.Port, User: plan.User, PasswordRef: plan.PasswordRef,
		TLSMode: plan.TLSMode, TLSRootCert: plan.TLSRootCert, ClientPath: plan.ClientPath,
	}}
}

// plan is the connection of the record as a plan, for the connection string.
func (r pgRecord) plan() sdk.PostgresRestore {
	c := r.Connection
	return sdk.PostgresRestore{Host: c.Host, Port: c.Port, User: c.User, PasswordRef: c.PasswordRef, TLSMode: c.TLSMode, TLSRootCert: c.TLSRootCert, ClientPath: c.ClientPath}
}

// created reports whether some object of the record has an OID: the restore
// went as far as creating it.
func (r pgRecord) created() bool {
	return r.Database.OID != 0 || slices.ContainsFunc(r.Roles, func(o pgObject) bool { return o.OID != 0 })
}

// pgJournal is the directory of the rollback journal, held open.
type pgJournal struct {
	dir hostsetup.Dir
}

// openJournal opens the journal directory below executor.state_dir, and makes
// it (0700) if create is set; a missing one is fs.ErrNotExist otherwise.
func (e *Engine) openJournal(create bool) (*pgJournal, error) {
	path := filepath.Join(e.opts.Postgres.StateDir, journalDirName)
	var mk *hostsetup.Make
	if create {
		mk = &hostsetup.Make{Parents: e.opts.Owner, Last: e.opts.Owner}
	}
	dir, _, err := hostsetup.OpenDir(e.opts.FS, path, mk)
	if err != nil {
		return nil, err
	}
	return &pgJournal{dir: dir}, nil
}

func (j *pgJournal) close() { _ = j.dir.Close() }

func fileOf(commandID string) string { return commandID + journalSuffix }

// save writes the record whole or not at all: a temporary file, fsync, rename,
// fsync of the directory.
func (j *pgJournal) save(r pgRecord) (err error) {
	data, _ := json.Marshal(r) // plain fields: Marshal cannot fail
	name := fileOf(r.CommandID)
	tmp := name + journalTemp
	defer func() {
		if err != nil {
			_ = j.dir.Remove(tmp)
		}
	}()
	_ = j.dir.Remove(tmp) // a leftover of a crash
	f, err := j.dir.CreateFile(tmp, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := j.dir.Rename(tmp, name); err != nil {
		return err
	}
	return j.dir.Sync()
}

// remove deletes the entry of a command; one that is not there is no error.
func (j *pgJournal) remove(commandID string) error {
	if err := j.dir.Remove(fileOf(commandID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_ = j.dir.Remove(fileOf(commandID) + journalTemp)
	return j.dir.Sync()
}

// load reads the entry of a command.
func (j *pgJournal) load(commandID string) (pgRecord, error) {
	f, err := j.dir.OpenFile(fileOf(commandID))
	if err != nil {
		return pgRecord{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return pgRecord{}, err
	}
	var r pgRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return pgRecord{}, fmt.Errorf("the rollback journal entry %s is unreadable: %w", fileOf(commandID), err)
	}
	return r, nil
}

// commands are the commands that have an entry, sorted.
func (j *pgJournal) commands() ([]string, error) {
	names, err := j.dir.Names()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, n := range names {
		if id, ok := strings.CutSuffix(n, journalSuffix); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// entry opens the journal entry of a command; ok is false when there is none
// (or nothing can be done about it). The caller closes the journal.
func (e *Engine) entry(commandID string) (j *pgJournal, rec pgRecord, ok bool) {
	j, ok = e.journalOf(commandID)
	if !ok {
		return nil, pgRecord{}, false
	}
	rec, err := j.load(commandID)
	if err != nil {
		j.close()
		return nil, pgRecord{}, false
	}
	return j, rec, true
}

// journalOf opens the journal for the entry of a command: not for a name that
// cannot be one, and not for a host that keeps no journal.
func (e *Engine) journalOf(commandID string) (*pgJournal, bool) {
	if e.opts.Postgres.StateDir == "" || !UsableName(commandID) {
		return nil, false
	}
	j, err := e.openJournal(false)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.opts.Logger.Error("cannot open the rollback journal of restores of databases", "error", err)
	}
	return j, err == nil
}

// removeEntry removes the entry of a command and logs it.
func (e *Engine) removeEntry(j *pgJournal, commandID string) {
	if err := j.remove(commandID); err != nil {
		e.opts.Logger.Error("cannot remove the rollback journal entry of a restore of a database; remove it by hand", "command_id", commandID, "error", err)
		return
	}
	e.opts.Logger.Info("removed the rollback journal entry of a restore of a database", "command_id", commandID)
}

// settleDatabase removes the journal entry of a command whose result is
// journaled by the executor (ВК27): a restore of a database that completed has
// nothing more to roll back. An entry that is not there is nothing to settle.
func (e *Engine) settleDatabase(commandID string) {
	if j, _, ok := e.entry(commandID); ok {
		defer j.close()
		e.removeEntry(j, commandID)
	}
}

// Abandon drops the journal entry of a command whose failed result is
// journaled, if the entry holds nothing the next start could still drop: only
// intents, which are never dropped by name. An entry with an OID stays.
func (e *Engine) Abandon(commandID string) {
	if j, rec, ok := e.entry(commandID); ok {
		defer j.close()
		if !rec.created() {
			e.removeEntry(j, commandID)
		}
	}
}
