// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// maintenanceDB is the database of the target every check and every statement
// that creates or drops a database signs in to.
const maintenanceDB = "postgres"

// probeScript asks the target who the role is and what it may do. The version
// goes first so that a server without a column the rest needs still answers.
const probeScript = `select current_setting('server_version_num'), r.rolsuper, r.rolcreatedb, current_user from pg_roles r where r.rolname = current_user;`

// pgProbe is what the target answered to probeScript.
type pgProbe struct {
	server   pgVersion
	super    bool
	createdb bool
	user     string
}

// parseProbe reads the row: version number, superuser, CREATEDB, role (last,
// it may hold any character).
func parseProbe(out outcome) (pgProbe, error) {
	line := out.last()
	f := strings.SplitN(line, "|", 4)
	if len(f) != 4 {
		return pgProbe{}, fmt.Errorf("unexpected answer of psql: %q", line)
	}
	num, err := strconv.Atoi(f[0])
	if err != nil {
		return pgProbe{}, fmt.Errorf("unexpected answer of psql: %q", line)
	}
	return pgProbe{server: serverVersion(num), super: f[1] == "t", createdb: f[2] == "t", user: f[3]}, nil
}

// checkConnection signs in to the maintenance database of the target (ВК22).
func (a *pgAttempt) checkConnection() *Failure {
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, probeScript)
	if a.ctx.Err() != nil {
		return a.interrupted(StageCheck)
	}
	where := fmt.Sprintf("%s:%d as %s", a.req.Plan.Host, a.req.Plan.Port, a.req.Plan.User)
	if !o.ok() {
		return &Failure{Reason: refusal.PGConnect, Stage: StageCheck, Detail: fmt.Sprintf("cannot query the PostgreSQL server %s: %s", where, o.text())}
	}
	probe, err := parseProbe(o)
	if err != nil {
		return &Failure{Reason: refusal.PGConnect, Stage: StageCheck, Detail: fmt.Sprintf("cannot query the PostgreSQL server %s: %v", where, err)}
	}
	a.probe = probe
	return nil
}

// checkServer: the target is supported and not older than the source (ВК22,
// РВ2). Without the tag of the server of the snapshot only the minimum is
// checked, and a WARN says so.
func (a *pgAttempt) checkServer() *Failure {
	server := a.probe.server
	if server.major < minServerMajor {
		return &Failure{Reason: refusal.PGServerTooOld, Stage: StageCheck,
			Detail: fmt.Sprintf("PostgreSQL %s is not supported: the oldest supported server is %d", server.text, minServerMajor)}
	}
	tag, known := parseTag(a.plan.ServerVersion)
	if !known {
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_WARN, "the snapshot has no tag postgresql.server_version: the version of the target server is not checked against it")
		return nil
	}
	if server.major < tag.major {
		return &Failure{Reason: refusal.PGServerTooOld, Stage: StageCheck,
			Detail: fmt.Sprintf("the target server %s is older than %s, the server the snapshot was taken from: restore into a server of version %d or newer", server.text, tag.text, tag.major)}
	}
	return nil
}

// checkRole: the role may create a database, and global objects need a
// superuser (ВК22).
func (a *pgAttempt) checkRole() *Failure {
	p := a.probe
	switch {
	case !p.super && !p.createdb:
		return &Failure{Reason: refusal.CreatedbRequired, Stage: StageCheck,
			Detail: fmt.Sprintf("role %q is not a superuser and has no CREATEDB: grant it (ALTER ROLE %s CREATEDB) or restore as another role", p.user, quoteIdent(p.user))}
	case a.req.Plan.Globals && !p.super:
		return &Failure{Reason: refusal.SuperuserRequired, Stage: StageCheck,
			Detail: fmt.Sprintf("role %q is not a superuser: restore_globals creates roles, which belong to the whole cluster, and needs one", p.user)}
	}
	return nil
}

// quoteIdent is an identifier as SQL writes it, for the hints of messages.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// systemIDScript reads the identifier of the cluster; every role may by default.
const systemIDScript = `select system_identifier from pg_control_system();`

// compareCluster warns when the target is the cluster the snapshot was taken
// from (РВ8, ВК29). A snapshot without the tag, or a target that does not
// tell, is not compared: an INFO line says so, and the restore goes on.
func (a *pgAttempt) compareCluster() *Failure {
	id, why := a.targetIdentifier()
	switch {
	case a.ctx.Err() != nil:
		return a.interrupted(StageCheck)
	case id == "":
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "the target cluster was not compared with the one of the snapshot: "+why)
	case a.plan.SystemIdentifier == "":
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "the target cluster was not compared with the one of the snapshot: the snapshot has no tag postgresql.system_identifier")
	case id == a.plan.SystemIdentifier:
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_WARN, fmt.Sprintf("the target is the cluster the snapshot was taken from (system identifier %s)", id))
	}
	return nil
}

// targetIdentifier is the identifier of the target cluster; "" and why when it
// cannot be read.
func (a *pgAttempt) targetIdentifier() (id, why string) {
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, systemIDScript)
	id = o.last()
	switch {
	case !o.ok():
		return "", "the role cannot read pg_control_system(): " + o.text()
	case strings.Trim(id, "0123456789") != "" || id == "":
		return "", fmt.Sprintf("unexpected answer of psql: %q", id)
	}
	return id, ""
}
