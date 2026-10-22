// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// rolesScript lists the roles the file creates that exist on the target, and
// the bootstrap superuser (OID 10), which the target always has.
const rolesScript = `select r.oid = 10, r.rolname from pg_roles r where r.oid = 10 or r.rolname = any(string_to_array(:'names', chr(1)));`

// checkGlobals reads the file of the global objects and judges it before any
// change (ВК26): no psql commands, no tablespaces, no role that exists.
func (a *restoreRun) checkGlobals() *sdk.RestoreError {
	if !a.globals {
		return nil
	}
	content, f := a.readGlobals()
	if f != nil {
		return f
	}
	g := parseGlobals(content)
	if f := a.refuseGlobals(g); f != nil {
		return f
	}
	bootstrap, f := a.checkRoles(g.roles)
	if f != nil {
		return f
	}
	a.script = g.without(bootstrap)
	if slices.Contains(g.roles, bootstrap) {
		a.h.Log(sdk.LevelInfo, fmt.Sprintf("the statements about the role %q are skipped: it is the bootstrap superuser of the target cluster", bootstrap))
	}
	a.roles = slices.DeleteFunc(slices.Clone(g.roles), func(n string) bool { return n == bootstrap })
	return nil
}

// checkRoles: none of the roles the file creates exists on the target, except
// the bootstrap superuser, whose name it returns.
func (a *restoreRun) checkRoles(names []string) (bootstrap string, f *sdk.RestoreError) {
	bootstrap, existing, f := a.existingRoles(names)
	if len(existing) > 0 { // existingRoles lists none when it fails
		f = a.fail(ReasonRolesExist, stageCheck,
			"the roles %s already exist on the target server and the restore does not change an existing role; restore without restore_globals", quoted(existing))
	}
	return bootstrap, f
}

// refuseGlobals: the file holds a command of psql, or creates a tablespace.
func (a *restoreRun) refuseGlobals(g globals) *sdk.RestoreError {
	if g.meta != 0 {
		return a.fail(ReasonGlobalsFailed, stageCheck, "line %d of the file of the global objects is a command of psql, not SQL: the file is refused", g.meta)
	}
	if len(g.tablespaces) > 0 {
		return a.fail(ReasonGlobalsTablespaces, stageCheck,
			"the global objects create the tablespaces %s: a tablespace cannot be created in a transaction and lies in a directory of the source host; restore without restore_globals (the database then goes without tablespaces) or create them by hand", quoted(g.tablespaces))
	}
	return nil
}

// readGlobals reads the file of the global objects of the plan out of restic.
func (a *restoreRun) readGlobals() (string, *sdk.RestoreError) {
	file := a.layout.globals
	buf := &limitedBuffer{max: maxGlobalsBytes}
	if err := a.h.Dump(a.ctx, file.snapshotID, file.path, buf); err != nil {
		if a.ctx.Err() != nil {
			return "", a.interrupted(stageCheck)
		}
		if buf.exceeded {
			return "", refuse(ReasonGlobalsFailed, stageCheck, "the file of the global objects is larger than %d MiB", maxGlobalsBytes>>20)
		}
		return "", staged(err, stageCheck)
	}
	return buf.String(), nil
}

// existingRoles asks the target which of the roles exist; it returns the name
// of the bootstrap superuser and the existing roles other than it.
func (a *restoreRun) existingRoles(names []string) (bootstrap string, existing []string, f *sdk.RestoreError) {
	o := a.conn.psql(a.ctx, maintenanceDB, rolesScript, "names", strings.Join(names, "\x01"))
	if !o.ok() {
		return "", nil, a.fail(ReasonConnect, stageCheck, "cannot read the roles of the target server: %s", o.text())
	}
	for _, line := range o.lines() {
		if strings.TrimSpace(line) == "" {
			continue
		}
		isBootstrap, name, ok := strings.Cut(line, "|")
		switch {
		case !ok:
			return "", nil, a.fail(ReasonConnect, stageCheck, "unexpected answer of psql: %q", line)
		case isBootstrap == "t":
			bootstrap = name
		default:
			existing = append(existing, name)
		}
	}
	return bootstrap, existing, nil
}

// existsScript asks whether the database exists.
const existsScript = `select exists(select 1 from pg_database where datname = :'dbname');`

// checkNewName: no database of the name exists on the target (ВК22).
func (a *restoreRun) checkNewName() *sdk.RestoreError {
	o := a.conn.psql(a.ctx, maintenanceDB, existsScript, "dbname", a.database)
	switch {
	case !o.ok():
		return a.fail(ReasonConnect, stageCheck, "cannot look for the database %q: %s", a.database, o.text())
	case o.last() == "t":
		return a.fail(ReasonDatabaseExists, stageCheck, "the database %q already exists on the target server: the restore never writes into an existing database; choose a new name", a.database)
	}
	return nil
}

// capacityScript is the size of the lock table of the target cluster: one
// transaction may take as many locks as it has objects to create (ВК28).
const capacityScript = `select current_setting('max_locks_per_transaction')::bigint * (current_setting('max_connections')::bigint + current_setting('max_prepared_transactions')::bigint);`

// chooseMode reads the table of contents of the dump and decides whether
// pg_restore runs in one transaction: only if the objects take less than the
// share the host allows of the lock table, which all the applications of the
// cluster share (РВ1, ВК28).
func (a *restoreRun) chooseMode() *sdk.RestoreError {
	objects, f := a.countObjects()
	if f != nil {
		return f
	}
	o := a.conn.psql(a.ctx, maintenanceDB, capacityScript)
	capacity, ok := o.number(64)
	if !ok {
		return a.fail(ReasonConnect, stageCheck, "cannot read the size of the lock table of the target server: %s", o.problem())
	}
	share := a.p.lockShare()
	a.single = float64(objects) < share*float64(capacity)
	mode := "separate transactions"
	if a.single {
		mode = "single transaction"
	}
	a.h.Log(sdk.LevelInfo, fmt.Sprintf("pg_restore mode: %s (%d objects, lock capacity %d, share %g)", mode, objects, capacity, share))
	return nil
}

// countObjects is the number of entries of the table of contents of the dump:
// its head goes to pg_restore -l (the table lies at the start of an archive of
// the custom format), and restic dump is stopped.
func (a *restoreRun) countObjects() (n int, f *sdk.RestoreError) {
	res := a.stream([]string{"-l"}, true, nil)
	if f = a.streamFailure(res, stageCheck, "cannot read the table of contents of the dump"); f != nil {
		return n, f
	}
	for _, line := range res.o.lines() {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, ";") {
			n++
		}
	}
	return n, nil
}
