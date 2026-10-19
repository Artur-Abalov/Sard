// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// RestoreDatabase restores the snapshot of a database into a NEW database of
// the target server, or leaves no trace (R2b): the snapshot is checked, the
// global objects (optional) are applied in one transaction, the database is
// created from template0 and filled by pg_restore from the stream of restic
// dump. What the step created is dropped by OID when anything fails; the
// rollback journal says what that is, also after a kill of the agent.
func (e *Engine) RestoreDatabase(ctx context.Context, repo DatabaseRepository, req DatabaseRequest, r Reporter) (DatabaseOutput, *Failure) {
	if err := (sdk.RestorePlan{Database: &req.Plan}).Validate(); err != nil {
		return DatabaseOutput{}, &Failure{Reason: refusal.TargetInvalid, Stage: StageCheck, Detail: "the restore plan is invalid: " + err.Error()}
	}
	if !UsableName(req.CommandID) {
		return DatabaseOutput{}, &Failure{Reason: refusal.TargetInvalid, Stage: StageCheck, Detail: fmt.Sprintf("command id %q cannot name a journal entry", req.CommandID)}
	}
	a := &pgAttempt{e: e, ctx: ctx, repo: repo, req: req, r: r, target: e.targetOf(req.Repository)}
	a.conn = pgConn{plan: req.Plan, password: req.Password, environ: e.environ(), exec: e.opts.Postgres.Exec}
	defer a.close()
	r.Progress(agentv1.StepPhase_STEP_PHASE_PREPARING, 0, 0)
	if f := a.prepare(); f != nil {
		return DatabaseOutput{}, f
	}
	r.Progress(agentv1.StepPhase_STEP_PHASE_RESTORING, 0, 0)
	out, f := a.restore()
	if f != nil {
		return DatabaseOutput{}, a.rollback(f)
	}
	return out, nil
}

func (e *Engine) environ() func() []string {
	if e.opts.Postgres.Environ != nil {
		return e.opts.Postgres.Environ
	}
	return func() []string { return nil }
}

// pgAttempt is one restore of a database in progress.
type pgAttempt struct {
	e      *Engine
	ctx    context.Context
	repo   DatabaseRepository
	req    DatabaseRequest
	r      Reporter
	target repoinit.Target
	conn   pgConn

	snap restore.Snapshot
	plan restore.PGPlan
	// script is the file of the global objects as it is applied, roles the
	// roles it creates (ВК26).
	script string
	roles  []string
	// single: pg_restore runs in one transaction (ВК28).
	single bool
	// probe is what the target told about itself and the role.
	probe pgProbe
	// out is the outcome, once the restore has finished.
	out DatabaseOutput
	// globalsApplied, dbCreated: the transaction of the global objects was
	// committed, CREATE DATABASE succeeded; the rollback looks up the OIDs it
	// could not record.
	globalsApplied, dbCreated bool

	journal *pgJournal
	rec     pgRecord
}

func (a *pgAttempt) close() {
	if a.journal != nil {
		a.journal.close()
	}
}

func (a *pgAttempt) name() string { return a.req.Plan.Name }

// interrupted is the failure of a step whose context ended, at stage.
func (a *pgAttempt) interrupted(stage Stage) *Failure {
	return asFailure(repoinit.Interruption(a.ctx), stage, a.target)
}

// fail is a failure at stage; an ended context outranks it.
func (a *pgAttempt) fail(reason refusal.Reason, stage Stage, format string, args ...any) *Failure {
	if a.ctx.Err() != nil {
		return a.interrupted(stage)
	}
	return &Failure{Reason: reason, Stage: stage, Detail: fmt.Sprintf(format, args...)}
}

// prepare runs the checks of ВК22 in order; the first that fails names the
// reason. Nothing is changed on the target.
func (a *pgAttempt) prepare() *Failure {
	for _, check := range []func() *Failure{
		a.checkSnapshot, a.checkClient, a.checkConnection, a.checkServer, a.checkRole,
		a.compareCluster, a.checkGlobals, a.checkNewName, a.chooseMode,
	} {
		if f := check(); f != nil {
			return f
		}
	}
	return nil
}

// checkSnapshot: the snapshot of the step is a snapshot of a database of the
// source, with the global objects it was asked for (ВК21).
func (a *pgAttempt) checkSnapshot() *Failure {
	list, err := restore.List(a.ctx, a.repo, a.req.Source)
	if err != nil {
		return asFailure(restore.ListFailure(a.ctx, err, a.target), StageCheck, a.target)
	}
	snap, failure := restore.Select(list, a.req.SnapshotID, a.req.Repository, a.req.Source)
	if failure == nil {
		return a.planSnapshot(list, snap)
	}
	return &Failure{Reason: failure.Reason, Stage: StageCheck, Detail: failure.Detail}
}

// planSnapshot lays out the snapshot, which must be one of a database.
func (a *pgAttempt) planSnapshot(list []restore.Snapshot, snap restore.Snapshot) *Failure {
	if restore.KindOf(snap) != restore.KindDatabase {
		return a.fail(refusal.ResticOutputUnexpected, StageCheck, "snapshot %s is not the snapshot of a PostgreSQL database (tag postgresql.part=database): choose the snapshot of the database", snap.ShortID)
	}
	plan, failure := restore.PlanPostgres(list, snap)
	if failure != nil {
		return &Failure{Reason: failure.Reason, Stage: StageCheck, Detail: failure.Detail}
	}
	a.snap, a.plan = snap, plan
	if a.req.Plan.Globals && !plan.Globals {
		return a.fail(refusal.GlobalsSnapshotMissing, StageCheck, "snapshot %s has no snapshot of global objects (postgresql.main_snapshot=%s): restore without restore_globals, or back the source up with include_globals", snap.ShortID, snap.ID)
	}
	return nil
}

// checkClient: pg_restore and psql are found, and pg_restore is not older than
// the pg_dump that made the snapshot (ВК22).
func (a *pgAttempt) checkClient() *Failure {
	tools, f := a.e.locate(a.req.Plan.ClientPath, a.e.environ()())
	if f != nil {
		return f
	}
	a.conn.tools = tools
	version, f := a.clientVersion()
	if f != nil {
		return f
	}
	tag, known := parseTag(a.plan.Version)
	switch {
	case !known:
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_WARN, "the snapshot has no tag postgresql.pg_dump_version: the version of pg_restore is not checked against it")
	case version.major < tag.major:
		return a.fail(refusal.PGClientTooOld, StageCheck, "pg_restore %s is older than pg_dump %s that made the snapshot: install pg_restore %d or newer (%s) or set pg_restore_path", version.text, tag.text, tag.major, packageHint)
	}
	return nil
}

// clientVersion asks pg_restore for its version.
func (a *pgAttempt) clientVersion() (pgVersion, *Failure) {
	tools := a.conn.tools
	o := a.conn.run(a.ctx, a.r, tools.restore, []string{"--version"}, nil)
	version, ok := parseClient(strings.Join(o.stdout, "\n"))
	switch {
	case a.ctx.Err() != nil:
		return pgVersion{}, a.interrupted(StageCheck)
	case !o.ok() || !ok:
		return pgVersion{}, a.fail(refusal.PGClientMissing, StageCheck, "cannot read the version of %s: %s; %s or set pg_restore_path", tools.restore, o.text(), packageHint)
	}
	return version, nil
}
