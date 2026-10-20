// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// restore is RESTORING (ВК23): the journal, the global objects, the database,
// the data. Every change is announced in the journal first.
func (a *pgAttempt) restore() (DatabaseOutput, *Failure) {
	if f := a.openJournal(); f != nil {
		return DatabaseOutput{}, f
	}
	for _, step := range []func() *Failure{a.applyGlobals, a.createDatabase, a.restoreData, a.finish} {
		if f := step(); f != nil {
			return DatabaseOutput{}, f
		}
	}
	return a.out, nil
}

// finish counts the tables and writes the outcome into the journal: after it a
// kill does not undo the restore (ВК27, completed).
func (a *pgAttempt) finish() *Failure {
	tables, f := a.countTables()
	if f != nil {
		return f
	}
	a.out = DatabaseOutput{Database: a.name(), Tables: tables, Roles: uint64(len(a.rec.Roles))}
	a.rec.Result = &pgResult{Database: a.out.Database, Tables: a.out.Tables, Roles: a.out.Roles}
	return a.save(StageRestore)
}

// openJournal writes the first entry of the journal, with the connection and
// the name of the secret, before the target is changed (ВК27).
func (a *pgAttempt) openJournal() *Failure {
	j, err := a.e.openJournal(true)
	if err != nil {
		return a.journalFailure(err, StageCheck)
	}
	a.journal = j
	a.rec = recordOf(a.req.CommandID, a.req.Plan)
	return a.save(StageCheck)
}

// save writes the entry; a journal that cannot be written stops the restore,
// because a kill could then leave what it creates unaccounted for.
func (a *pgAttempt) save(stage Stage) *Failure {
	if err := a.journal.save(a.rec); err != nil {
		return a.journalFailure(err, stage)
	}
	return nil
}

func (a *pgAttempt) journalFailure(err error, stage Stage) *Failure {
	return &Failure{Reason: refusal.TargetWrite, Stage: stage,
		Detail: fmt.Sprintf("cannot write the rollback journal in %s: %v; the state directory of the executor must be writable by the service", a.e.opts.Postgres.StateDir, err)}
}

// applyGlobals applies the global objects in one transaction that stops at the
// first error (ВК26): either all the roles exist afterwards, or none.
func (a *pgAttempt) applyGlobals() *Failure {
	if !a.req.Plan.Globals {
		return nil
	}
	for _, name := range a.roles {
		a.rec.Roles = append(a.rec.Roles, pgObject{Name: name})
	}
	if f := a.save(StageGlobals); f != nil {
		a.rec.Roles = nil // not announced, so not created
		return f
	}
	args := []string{"-X", "-w", "-q", "-v", "ON_ERROR_STOP=1", "--single-transaction", "--dbname=" + a.conn.conninfo(maintenanceDB)}
	o := a.conn.run(a.ctx, a.r, a.conn.tools.psql, args, strings.NewReader(a.script))
	if !o.ok() {
		// Only a script that psql refused is rolled back as a whole. Any other
		// end (a kill after COMMIT, a lost connection) leaves the outcome
		// unknown: the intents of the roles that exist stay in the journal and
		// the rollback names them.
		a.rec.Roles = a.unsettledRoles(o)
		_ = a.save(StageGlobals)
		return a.fail(refusal.PGGlobalsFailed, StageGlobals, "%s", o.text())
	}
	a.globalsApplied = true
	return a.recordRoles(a.ctx)
}

// unsettledRoles are the intents to keep after a psql that did not apply the
// script. A refused script created nothing. After any other end the target is
// asked, read-only, which of the roles exist: those were absent before the
// transaction (ВК26), so they are its own; if the target cannot tell, all stay.
func (a *pgAttempt) unsettledRoles(o outcome) []pgObject {
	if o.err == nil && o.code == scriptErrorExit {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), rollbackTimeout)
	defer cancel()
	q := a.conn.psql(ctx, a.r, maintenanceDB, rolesScript, "names", strings.Join(a.roles, "\x01"))
	if !q.ok() {
		return a.rec.Roles
	}
	var kept []pgObject
	for _, role := range a.rec.Roles {
		if slices.Contains(q.stdout, "f|"+role.Name) {
			kept = append(kept, role)
		}
	}
	return kept
}

// roleOIDScript reads the OID of a role by its name.
const roleOIDScript = `select oid from pg_roles where rolname = :'name';`

// recordRoles writes the OIDs of the roles the transaction created. The roles
// are the step's: they did not exist before its transaction (ВК26), so their
// names lead to them now. Roles with an OID are not asked again.
func (a *pgAttempt) recordRoles(ctx context.Context) *Failure {
	for i, role := range a.rec.Roles {
		if role.OID != 0 {
			continue
		}
		o := a.conn.psql(ctx, a.r, maintenanceDB, roleOIDScript, "name", role.Name)
		oid, err := strconv.ParseUint(o.last(), 10, 32)
		if !o.ok() || err != nil || oid == 0 {
			return a.fail(refusal.PGGlobalsFailed, StageGlobals, "the role %q was created but its OID cannot be read: %s", role.Name, o.problem())
		}
		a.rec.Roles[i].OID = uint32(oid)
	}
	return a.save(StageGlobals)
}

// createScript creates the database as a name, not as SQL: psql quotes the
// variable as an identifier.
const createScript = `CREATE DATABASE :"dbname" TEMPLATE template0;`

// databaseOIDScript reads the OID of the new database.
const databaseOIDScript = `select oid from pg_database where datname = :'dbname';`

// createDatabase creates the database from template0 (outside a transaction,
// as PostgreSQL requires) and records its OID.
func (a *pgAttempt) createDatabase() *Failure {
	a.rec.Database = pgObject{Name: a.name()}
	if f := a.save(StageCreateDB); f != nil {
		a.rec.Database = pgObject{} // not announced, so not created
		return f
	}
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, createScript, "dbname", a.name())
	if !o.ok() {
		return a.createFailure(o)
	}
	a.dbCreated = true
	return a.recordDatabase(a.ctx)
}

// recordDatabase writes the OID of the database the step created. The database
// is the step's: its CREATE succeeded, so the name leads to it now.
func (a *pgAttempt) recordDatabase(ctx context.Context) *Failure {
	o := a.conn.psql(ctx, a.r, maintenanceDB, databaseOIDScript, "dbname", a.name())
	oid, err := strconv.ParseUint(o.last(), 10, 32)
	if !o.ok() || err != nil || oid == 0 {
		return a.fail(refusal.PGCreateDatabase, StageCreateDB, "the database %q was created but its OID cannot be read: %s", a.name(), o.problem())
	}
	a.rec.Database.OID = uint32(oid)
	return a.save(StageCreateDB)
}

// scriptErrorExit is the exit code of psql that stopped at the first error of
// the script (ON_ERROR_STOP): the statement was refused, nothing was created.
const scriptErrorExit = 3

// createFailure: a name taken in between the check and the statement is
// DATABASE_EXISTS and the other database stays untouched; a statement the
// server refused leaves no database; any other end of psql leaves the outcome
// unknown, and the rollback does not drop what it cannot tell is its own.
func (a *pgAttempt) createFailure(o outcome) *Failure {
	refused := o.err == nil && o.code == scriptErrorExit
	if refused {
		a.rec.Database = pgObject{}
		_ = a.save(StageCreateDB)
	}
	if refused && strings.Contains(o.reason, "already exists") {
		return a.fail(refusal.DatabaseExists, StageCreateDB, "the database %q was created by someone else after the check and is left alone: %s", a.name(), o.reason)
	}
	return a.fail(refusal.PGCreateDatabase, StageCreateDB, "%s", o.text())
}

// restoreData fills the new database from the stream of restic dump. Without
// the global objects the owners, rights and tablespaces of the source are left
// out: the roles and the directories may not exist on the target.
func (a *pgAttempt) restoreData() *Failure {
	args := []string{"--exit-on-error", "--no-password"}
	if a.single {
		args = append(args, "--single-transaction")
	}
	if !a.req.Plan.Globals {
		args = append(args, "--no-owner", "--no-acl", "--no-tablespaces")
	}
	args = append(args, "--dbname="+a.conn.conninfo(a.name()))
	res := a.stream(args, false, func(total uint64) {
		a.r.Progress(agentv1.StepPhase_STEP_PHASE_RESTORING, total, 0)
	})
	return a.streamFailure(res, StageRestore, "pg_restore did not finish")
}

// tablesScript counts the tables of the new database.
const tablesScript = `select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relkind in ('r', 'p') and n.nspname not in ('pg_catalog', 'information_schema', 'pg_toast');`

func (a *pgAttempt) countTables() (uint64, *Failure) {
	o := a.conn.psql(a.ctx, a.r, a.name(), tablesScript)
	n, err := strconv.ParseUint(o.last(), 10, 64)
	if !o.ok() || err != nil {
		return 0, a.fail(refusal.PGRestoreFailed, StageRestore, "cannot count the tables of the new database: %s", o.problem())
	}
	return n, nil
}
