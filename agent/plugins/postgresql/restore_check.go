// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// maintenanceDB is the database of the target every check and every statement
// that creates or drops a database signs in to.
const maintenanceDB = "postgres"

// checkClient: pg_restore and psql are found, and pg_restore is not older than
// the pg_dump that made the snapshot (ВК22).
func (a *restoreRun) checkClient() *sdk.RestoreError {
	t, f := a.p.locateRestore(a.conn.to.ClientPath)
	if f != nil {
		return f
	}
	a.conn.tools = t
	v, f := a.clientVersion()
	if f != nil {
		return f
	}
	tag, known := parseTag(a.layout.version)
	switch {
	case !known:
		a.h.Log(sdk.LevelWarn, "the snapshot has no tag postgresql.pg_dump_version: the version of pg_restore is not checked against it")
	case v.major < tag.major:
		return a.fail(ReasonClientTooOld, stageCheck, "pg_restore %s is older than pg_dump %s that made the snapshot: install pg_restore %d or newer (%s) or set pg_restore_path", v.text, tag.text, tag.major, packageHint)
	}
	return nil
}

// clientVersion asks pg_restore for its version.
func (a *restoreRun) clientVersion() (version, *sdk.RestoreError) {
	pgRestore := a.conn.tools.pgRestore
	o := a.conn.run(a.ctx, pgRestore, []string{"--version"}, nil)
	v, ok := scanVersion(o.out)
	switch {
	case a.ctx.Err() != nil:
		return version{}, a.interrupted(stageCheck)
	case !o.ok() || !ok:
		f := refuse(ReasonClientMissing, stageCheck, "cannot read the version of %s: %s; %s or set pg_restore_path", pgRestore, o.text(), packageHint)
		f.Err = sdk.ErrProgramMissing
		return version{}, f
	}
	return v, nil
}

// checkConnection signs in to the maintenance database of the target (ВК22).
func (a *restoreRun) checkConnection() *sdk.RestoreError {
	o := a.conn.psql(a.ctx, maintenanceDB, restoreProbeScript)
	if a.ctx.Err() != nil {
		return a.interrupted(stageCheck)
	}
	to := a.conn.to
	where := fmt.Sprintf("%s:%d as %s", to.Host, to.Port, to.User)
	if !o.ok() {
		return refuse(ReasonConnect, stageCheck, "cannot query the PostgreSQL server %s: %s", where, o.text())
	}
	probe, err := parseRestoreProbe(o)
	if err != nil {
		return refuse(ReasonConnect, stageCheck, "cannot query the PostgreSQL server %s: %v", where, err)
	}
	a.probe = probe
	return nil
}

// checkServer: the target is supported and not older than the source (ВК22,
// РВ2). Without the tag of the server of the snapshot only the minimum is
// checked, and a WARN says so.
func (a *restoreRun) checkServer() *sdk.RestoreError {
	server := a.probe.server
	if server.major < minServerMajor {
		return refuse(ReasonServerTooOld, stageCheck, "PostgreSQL %s is not supported: the oldest supported server is %d", server.text, minServerMajor)
	}
	tag, known := parseTag(a.layout.serverVersion)
	if !known {
		a.h.Log(sdk.LevelWarn, "the snapshot has no tag postgresql.server_version: the version of the target server is not checked against it")
		return nil
	}
	if server.major < tag.major {
		return refuse(ReasonServerTooOld, stageCheck, "the target server %s is older than %s, the server the snapshot was taken from: restore into a server of version %d or newer", server.text, tag.text, tag.major)
	}
	return nil
}

// checkRole: the role may create a database, and global objects need a
// superuser (ВК22).
func (a *restoreRun) checkRole() *sdk.RestoreError {
	p := a.probe
	switch {
	case !p.super && !p.createdb:
		return refuse(ReasonCreatedbRequired, stageCheck,
			"role %q is not a superuser and has no CREATEDB: grant it (ALTER ROLE %s CREATEDB) or restore as another role", p.user, quoteIdent(p.user))
	case a.globals && !p.super:
		return refuse(ReasonSuperuserRequired, stageCheck,
			"role %q is not a superuser: restore_globals creates roles, which belong to the whole cluster, and needs one", p.user)
	}
	return nil
}

// quoteIdent is an identifier as SQL writes it, for the hints of messages.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// compareCluster warns when the target is the cluster the snapshot was taken
// from (РВ8, ВК29). A snapshot without the tag, or a target that does not
// tell, is not compared: an INFO line says so, and the restore goes on.
func (a *restoreRun) compareCluster() *sdk.RestoreError {
	id, why := a.targetIdentifier()
	switch {
	case a.ctx.Err() != nil:
		return a.interrupted(stageCheck)
	case id == "":
		a.h.Log(sdk.LevelInfo, "the target cluster was not compared with the one of the snapshot: "+why)
	case a.layout.systemID == "":
		a.h.Log(sdk.LevelInfo, "the target cluster was not compared with the one of the snapshot: the snapshot has no tag postgresql.system_identifier")
	case id == a.layout.systemID:
		a.h.Log(sdk.LevelWarn, fmt.Sprintf("the target is the cluster the snapshot was taken from (system identifier %s)", id))
	}
	return nil
}

// targetIdentifier is the identifier of the target cluster; "" and why when it
// cannot be read.
func (a *restoreRun) targetIdentifier() (id, why string) {
	o := a.conn.psql(a.ctx, maintenanceDB, systemIDQuery+";")
	id = o.last()
	switch {
	case !o.ok():
		return "", "the role cannot read pg_control_system(): " + o.text()
	case !isDigits(id):
		return "", fmt.Sprintf("unexpected answer of psql: %q", id)
	}
	return id, ""
}
