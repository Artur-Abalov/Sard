// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// rollbackTimeout bounds the rollback of a restore whose own context ended.
const rollbackTimeout = 5 * time.Minute

// rollback drops what the attempt created: the database, then the roles (ВК23),
// each by the OID the journal recorded (ВК16). What it cannot drop turns the
// failure into CLEANUP_FAILED with the leftover (ВК15), and stays in the
// journal for the next start; a rollback that drops everything removes the entry.
func (a *pgAttempt) rollback(f *Failure) *Failure {
	if a.journal == nil {
		return f
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), rollbackTimeout)
	defer cancel()
	a.resolveOIDs(ctx)
	left := a.e.undo(ctx, a.conn, a.r, a.rec)
	if len(left.leftover) == 0 {
		if err := a.journal.remove(a.req.CommandID); err != nil {
			a.e.opts.Logger.Warn("the rollback journal entry of a restore could not be removed", "command_id", a.req.CommandID, "error", err)
		}
		return f
	}
	a.rec = left.rest
	_ = a.journal.save(a.rec)
	f.Leftover = left.leftover
	f.Cleanup = strings.Join(left.causes, "; ")
	a.e.opts.Logger.Error("the rollback of a restore of a database could not drop what it created; remove it by hand",
		"command_id", a.req.CommandID, "left", strings.Join(left.leftover, ", "), "error", f.Cleanup)
	return f
}

// resolveOIDs records the OIDs that the step could not (it was stopped between
// the creation of an object and the read of its OID): what the step created
// itself is dropped like the rest, not left behind.
func (a *pgAttempt) resolveOIDs(ctx context.Context) {
	if a.globalsApplied {
		_ = a.recordRoles(ctx)
	}
	if a.dbCreated && a.rec.Database.OID == 0 {
		_ = a.recordDatabase(ctx)
	}
}

// undone is the outcome of a rollback.
type undone struct {
	// rest is the journal entry of what is left, leftover and causes the words
	// for the operator.
	rest     pgRecord
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
func (e *Engine) undo(ctx context.Context, c pgConn, r Reporter, rec pgRecord) undone {
	u := undone{rest: pgRecord{CommandID: rec.CommandID, Connection: rec.Connection}}
	if rec.Database.Name != "" {
		if !e.dropObject(ctx, c, r, objectKind{"database", dropDatabaseCheck, dropDatabase, "datname"}, rec.Database, &u) {
			u.rest.Database = rec.Database
		}
	}
	for _, role := range rec.Roles {
		if !e.dropObject(ctx, c, r, objectKind{"role", dropRoleCheck, dropRole, "rolname"}, role, &u) {
			u.rest.Roles = append(u.rest.Roles, role)
		}
	}
	return u
}

// objectKind is a kind of object the rollback drops: its words and scripts.
type objectKind struct {
	word, check, drop, column string
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
func (e *Engine) dropObject(ctx context.Context, c pgConn, r Reporter, kind objectKind, obj pgObject, u *undone) bool {
	label := kind.word + " " + obj.Name
	if obj.OID == 0 {
		u.keep(label, "the restore did not record the OID of the "+kind.word+", so it is not dropped by its name")
		return false
	}
	oid := fmt.Sprint(obj.OID)
	o := c.psql(ctx, r, maintenanceDB, kind.check, "name", obj.Name, "oid", oid)
	switch same, found, ok := strings.Cut(o.last(), "|"); {
	case !o.ok() || !ok:
		u.keep(label, o.problem())
	case found == "0":
		return true // gone already
	case same == "0":
		u.keep(fmt.Sprintf("%s (oid %d)", label, obj.OID), fmt.Sprintf("the %s with oid %d is now called otherwise, so it is not dropped by the recorded name", kind.word, obj.OID))
	default:
		return e.drop(ctx, c, r, kind, obj, label, u)
	}
	return false
}

// drop runs the DROP and tells the log.
func (e *Engine) drop(ctx context.Context, c pgConn, r Reporter, kind objectKind, obj pgObject, label string, u *undone) bool {
	o := c.psql(ctx, r, maintenanceDB, kind.drop, "name", obj.Name)
	if !o.ok() {
		u.keep(label, "DROP "+strings.ToUpper(kind.word)+": "+o.why())
		return false
	}
	r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "dropped the "+label)
	return true
}

// logReporter sends the lines of the programs of a rollback made at the start of
// the agent to its log: there is no step to report to.
type logReporter struct{ log *slog.Logger }

func (logReporter) Progress(agentv1.StepPhase, uint64, uint64) {}
func (logReporter) ProgressFiles(agentv1.StepPhase, uint64, uint64, uint64, uint64) {
}
func (l logReporter) Log(level agentv1.LogLevel, text string) {
	if level == agentv1.LogLevel_LOG_LEVEL_WARN {
		l.log.Warn(text)
		return
	}
	l.log.Info(text)
}
