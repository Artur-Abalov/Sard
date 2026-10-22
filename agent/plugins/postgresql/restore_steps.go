// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// restore is RESTORING (ВК23): the journal, the global objects, the database,
// the data. Every change is announced in the journal first.
func (a *restoreRun) restore() (sdk.RestoreResult, *sdk.RestoreError) {
	if f := a.openJournal(); f != nil {
		return sdk.RestoreResult{}, f
	}
	for _, step := range []func() *sdk.RestoreError{a.applyGlobals, a.createDatabase, a.restoreData, a.finish} {
		if f := step(); f != nil {
			return sdk.RestoreResult{}, f
		}
	}
	return a.out, nil
}

// finish counts the tables and saves the outcome in the journal: after it a
// kill does not undo the restore (ВК27, completed).
func (a *restoreRun) finish() *sdk.RestoreError {
	tables, f := a.countTables()
	if f != nil {
		return f
	}
	roles := uint64(len(a.entry.Roles))
	a.out = sdk.RestoreResult{Target: a.database, Database: a.database, Tables: tables, Roles: roles}
	a.entry.Result = &journalResult{Database: a.database, Tables: tables, Roles: roles}
	return a.save(stageRestore)
}

// openJournal saves the first entry of the journal, with the connection and
// the name of the secret, before the target is changed (ВК27).
func (a *restoreRun) openJournal() *sdk.RestoreError {
	return a.save(stageCheck)
}

// save saves the entry; a journal that cannot be written stops the restore,
// because a kill could then leave what it creates unaccounted for.
func (a *restoreRun) save(stage string) *sdk.RestoreError {
	if err := a.h.Journal().Save(a.entry.bytes()); err != nil {
		return refuse(ReasonTargetWrite, stage, "cannot write the rollback journal: %v; the state directory of the executor must be writable by the service", err)
	}
	return nil
}

// applyGlobals applies the global objects in one transaction that stops at the
// first error (ВК26): either all the roles exist afterwards, or none.
func (a *restoreRun) applyGlobals() *sdk.RestoreError {
	if !a.globals {
		return nil
	}
	for _, name := range a.roles {
		a.entry.Roles = append(a.entry.Roles, journalObject{Name: name})
	}
	if f := a.save(stageGlobals); f != nil {
		a.entry.Roles = nil // not announced, so not created
		return f
	}
	args := []string{"-X", "-w", "-q", "-v", "ON_ERROR_STOP=1", "--single-transaction", "--dbname=" + a.conn.to.conninfo(maintenanceDB)}
	o := a.conn.run(a.ctx, a.conn.tools.psql, args, strings.NewReader(a.script))
	if !o.ok() {
		// Only a script that psql refused is rolled back as a whole. Any other
		// end (a kill after COMMIT, a lost connection) leaves the outcome
		// unknown: the intents of the roles that exist stay in the journal and
		// the rollback names them.
		a.entry.Roles = a.unsettledRoles(o)
		_ = a.save(stageGlobals)
		return a.fail(ReasonGlobalsFailed, stageGlobals, "%s", o.text())
	}
	a.globalsApplied = true
	return a.recordRoles(a.ctx)
}

// unsettledRoles are the intents to keep after a psql that did not apply the
// script. A refused script created nothing. After any other end the target is
// asked, read-only, which of the roles exist: those were absent before the
// transaction (ВК26), so they are its own; if the target cannot tell, all stay.
func (a *restoreRun) unsettledRoles(o outcome) []journalObject {
	if o.err == nil && o.code == scriptErrorExit {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), rollbackTimeout)
	defer cancel()
	q := a.conn.psql(ctx, maintenanceDB, rolesScript, "names", strings.Join(a.roles, "\x01"))
	if !q.ok() {
		return a.entry.Roles
	}
	existing := q.lines()
	var kept []journalObject
	for _, role := range a.entry.Roles {
		if slices.Contains(existing, "f|"+role.Name) {
			kept = append(kept, role)
		}
	}
	return kept
}

// roleOIDScript reads the OID of a role by its name.
const roleOIDScript = `select oid from pg_roles where rolname = :'name';`

// recordRoles saves the OIDs of the roles the transaction created. The roles
// are the step's: they did not exist before its transaction (ВК26), so their
// names lead to them now. Roles with an OID are not asked again.
func (a *restoreRun) recordRoles(ctx context.Context) *sdk.RestoreError {
	for i, role := range a.entry.Roles {
		if role.OID != 0 {
			continue
		}
		o := a.conn.psql(ctx, maintenanceDB, roleOIDScript, "name", role.Name)
		oid, ok := o.number(32)
		if !ok || oid == 0 {
			return a.fail(ReasonGlobalsFailed, stageGlobals, "the role %q was created but its OID cannot be read: %s", role.Name, o.problem())
		}
		a.entry.Roles[i].OID = uint32(oid)
	}
	return a.save(stageGlobals)
}

// createScript creates the database as a name, not as SQL: psql quotes the
// variable as an identifier.
const createScript = `CREATE DATABASE :"dbname" TEMPLATE template0;`

// databaseOIDScript reads the OID of the new database.
const databaseOIDScript = `select oid from pg_database where datname = :'dbname';`

// createDatabase creates the database from template0 (outside a transaction,
// as PostgreSQL requires) and saves its OID.
func (a *restoreRun) createDatabase() *sdk.RestoreError {
	a.entry.Database = journalObject{Name: a.database}
	if f := a.save(stageCreateDB); f != nil {
		a.entry.Database = journalObject{} // not announced, so not created
		return f
	}
	o := a.conn.psql(a.ctx, maintenanceDB, createScript, "dbname", a.database)
	if !o.ok() {
		return a.createFailure(o)
	}
	a.dbCreated = true
	return a.recordDatabase(a.ctx)
}

// recordDatabase saves the OID of the database the step created. The database
// is the step's: its CREATE succeeded, so the name leads to it now.
func (a *restoreRun) recordDatabase(ctx context.Context) *sdk.RestoreError {
	o := a.conn.psql(ctx, maintenanceDB, databaseOIDScript, "dbname", a.database)
	oid, ok := o.number(32)
	if !ok || oid == 0 {
		return a.fail(ReasonCreateDatabase, stageCreateDB, "the database %q was created but its OID cannot be read: %s", a.database, o.problem())
	}
	a.entry.Database.OID = uint32(oid)
	return a.save(stageCreateDB)
}

// scriptErrorExit is the exit code of psql that stopped at the first error of
// the script (ON_ERROR_STOP): the statement was refused, nothing was created.
const scriptErrorExit = 3

// createFailure: a name taken in between the check and the statement is
// DATABASE_EXISTS and the other database stays untouched; a statement the
// server refused leaves no database; any other end of psql leaves the outcome
// unknown, and the rollback does not drop what it cannot tell is its own.
func (a *restoreRun) createFailure(o outcome) *sdk.RestoreError {
	refused := o.err == nil && o.code == scriptErrorExit
	if refused {
		a.entry.Database = journalObject{}
		_ = a.save(stageCreateDB)
	}
	if refused && strings.Contains(o.reason, "already exists") {
		return a.fail(ReasonDatabaseExists, stageCreateDB, "the database %q was created by someone else after the check and is left alone: %s", a.database, o.reason)
	}
	return a.fail(ReasonCreateDatabase, stageCreateDB, "%s", o.text())
}

// restoreData fills the new database from the stream of restic dump. Without
// the global objects the owners, rights and tablespaces of the source are left
// out: the roles and the directories may not exist on the target.
func (a *restoreRun) restoreData() *sdk.RestoreError {
	args := []string{"--exit-on-error", "--no-password"}
	if a.single {
		args = append(args, "--single-transaction")
	}
	if !a.globals {
		args = append(args, "--no-owner", "--no-acl", "--no-tablespaces")
	}
	args = append(args, "--dbname="+a.conn.to.conninfo(a.database))
	res := a.stream(args, false, func(total uint64) {
		a.h.Progress(total, 0)
	})
	return a.streamFailure(res, stageRestore, "pg_restore did not finish")
}

// tablesScript counts the tables of the new database.
const tablesScript = `select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relkind in ('r', 'p') and n.nspname not in ('pg_catalog', 'information_schema', 'pg_toast');`

func (a *restoreRun) countTables() (uint64, *sdk.RestoreError) {
	o := a.conn.psql(a.ctx, a.database, tablesScript)
	n, ok := o.number(64)
	if !ok {
		return n, a.fail(ReasonRestoreFailed, stageRestore, "cannot count the tables of the new database: %s", o.problem())
	}
	return n, nil
}
