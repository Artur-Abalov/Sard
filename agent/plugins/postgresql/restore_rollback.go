// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// rollbackTimeout bounds the rollback of a restore whose own context ended.
const rollbackTimeout = 5 * time.Minute

// rollback drops what the attempt created: the database, then the roles (ВК23),
// each by the OID the journal recorded (ВК16). What it cannot drop turns the
// failure into CLEANUP_FAILED with the leftover (ВК15), and the entry stays for
// the next start if it holds an OID to drop (the agent keeps it: Retain); a
// rollback that drops everything leaves the entry to the agent to remove.
func (a *restoreRun) rollback(f *sdk.RestoreError) *sdk.RestoreError {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), rollbackTimeout)
	defer cancel()
	a.resolveOIDs(ctx)
	left := undo(ctx, a.conn, a.entry)
	if len(left.leftover) == 0 {
		return f
	}
	a.entry = left.rest
	_ = a.save(stageRestore)
	f.Leftover = left.leftover
	f.Cleanup = strings.Join(left.causes, "; ")
	f.Retain = left.rest.created()
	return f
}

// resolveOIDs records the OIDs that the step could not (it was stopped between
// the creation of an object and the read of its OID): what the step created
// itself is dropped like the rest, not left behind.
func (a *restoreRun) resolveOIDs(ctx context.Context) {
	if a.globalsApplied {
		_ = a.recordRoles(ctx)
	}
	if a.dbCreated && a.entry.Database.OID == 0 {
		_ = a.recordDatabase(ctx)
	}
}

// undone is the outcome of a rollback.
type undone struct {
	// rest is the entry of what is left, leftover and causes the words for the
	// operator.
	rest     journalEntry
	leftover []string
	causes   []string
}

func (u *undone) keep(obj string, cause string) {
	u.leftover = append(u.leftover, obj)
	u.causes = append(u.causes, cause)
}

// undo drops the database and the roles the entry records. An object without
// an OID is only the intent to create it: it is not dropped by name, and is
// named as left (ВК16, РВ6).
func undo(ctx context.Context, c conn, e journalEntry) undone {
	u := undone{rest: newEntry(e.CommandID, e.Connection)}
	if e.Database.Name != "" {
		if !dropObject(ctx, c, objectKind{"database", dropDatabaseCheck, dropDatabase}, e.Database, &u) {
			u.rest.Database = e.Database
		}
	}
	for _, role := range e.Roles {
		if !dropObject(ctx, c, objectKind{"role", dropRoleCheck, dropRole}, role, &u) {
			u.rest.Roles = append(u.rest.Roles, role)
		}
	}
	return u
}

// objectKind is a kind of object the rollback drops: its words and scripts.
type objectKind struct {
	word, check, drop string
}

// The scripts of the rollback. The check counts the objects with the recorded
// OID and, of them, those that still have the recorded name.
const (
	dropDatabaseCheck = `select count(*) filter (where datname = :'name'), count(*) from pg_database where oid = :'oid';`
	dropDatabase      = `DROP DATABASE :"name" WITH (FORCE);`
	dropRoleCheck     = `select count(*) filter (where rolname = :'name'), count(*) from pg_roles where oid = :'oid';`
	dropRole          = `DROP ROLE :"name";`
)

// dropObject drops one object if the OID of the entry still leads to an object
// of its name; it reports whether the object is gone (dropped now, or dropped
// already). The rest goes to u.
func dropObject(ctx context.Context, c conn, kind objectKind, obj journalObject, u *undone) bool {
	label := kind.word + " " + obj.Name
	if obj.OID == 0 {
		u.keep(label, "the restore did not record the OID of the "+kind.word+", so it is not dropped by its name")
		return false
	}
	oid := fmt.Sprint(obj.OID)
	o := c.psql(ctx, maintenanceDB, kind.check, "name", obj.Name, "oid", oid)
	switch same, found, ok := strings.Cut(o.last(), "|"); {
	case !o.ok() || !ok:
		u.keep(label, o.problem())
	case found == "0":
		return true // gone already
	case same == "0":
		u.keep(fmt.Sprintf("%s (oid %d)", label, obj.OID), fmt.Sprintf("the %s with oid %d is now called otherwise, so it is not dropped by the recorded name", kind.word, obj.OID))
	default:
		return drop(ctx, c, kind, obj, label, u)
	}
	return false
}

// drop runs the DROP and tells the log.
func drop(ctx context.Context, c conn, kind objectKind, obj journalObject, label string, u *undone) bool {
	o := c.psql(ctx, maintenanceDB, kind.drop, "name", obj.Name)
	if !o.ok() {
		u.keep(label, "DROP "+strings.ToUpper(kind.word)+": "+o.why())
		return false
	}
	c.log.Log(sdk.LevelInfo, "dropped the "+label)
	return true
}
