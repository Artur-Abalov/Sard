// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.RestoreRunner = Plugin{}

// DefaultLockShare is the share of the lock table of the target cluster one
// restore may take when the host sets none (R2b ВК28).
const DefaultLockShare = 0.5

func (p Plugin) lockShare() float64 {
	if p.LockShare == nil {
		return DefaultLockShare
	}
	return *p.LockShare
}

// refuse is a restore that did not happen: the agent words it as
// "<reason> (<stage>): <detail>; nothing was restored".
func refuse(reason, stage, format string, args ...any) *sdk.RestoreError {
	return &sdk.RestoreError{Reason: reason, Stage: stage, Detail: fmt.Sprintf(format, args...)}
}

// staged is the failure of the host, which it worded already, at the stage.
func staged(err error, stage string) *sdk.RestoreError {
	var re *sdk.RestoreError
	if errors.As(sdk.AtStage(err, stage), &re) {
		return re
	}
	return &sdk.RestoreError{Reason: sdk.ReasonResticFailed, Stage: stage, Detail: err.Error(), Err: err}
}

// Restore implements sdk.RestoreRunner: it restores the snapshot of a database
// into a NEW database of the target server, or leaves no trace (R2b): the
// snapshot is checked, the global objects (optional) are applied in one
// transaction, the database is created from template0 and filled by pg_restore
// from the stream of restic dump. What the step created is dropped by OID when
// anything fails; the entry of the journal says what that is, also after a kill
// of the agent (Recover).
func (p Plugin) Restore(ctx context.Context, h sdk.RestoreHost, cfg sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
	a, f := p.newRestoreRun(ctx, h, cfg, req)
	if f != nil {
		return sdk.RestoreResult{}, f
	}
	h.Phase(sdk.PhasePreparing)
	if f := a.prepare(); f != nil {
		return sdk.RestoreResult{}, f
	}
	h.Phase(sdk.PhaseRestoring)
	out, f := a.restore()
	if f != nil {
		return sdk.RestoreResult{}, a.rollback(f)
	}
	return out, nil
}

// restoreRun is one restore of a database in progress.
type restoreRun struct {
	p    Plugin
	ctx  context.Context
	h    sdk.RestoreHost
	req  sdk.RestoreRequest
	conn conn
	// database is the new database, globals asks for the global objects.
	database string
	globals  bool

	snap   sdk.SnapshotInfo
	layout layout
	// script is the file of the global objects as it is applied, roles the
	// roles it creates (ВК26).
	script string
	roles  []string
	// single: pg_restore runs in one transaction (ВК28).
	single bool
	// probe is what the target told about itself and the role.
	probe restoreProbe
	// out is the outcome, once the restore has finished.
	out sdk.RestoreResult
	// globalsApplied, dbCreated: the transaction of the global objects was
	// committed, CREATE DATABASE succeeded; the rollback looks up the OIDs it
	// could not record.
	globalsApplied, dbCreated bool

	entry journalEntry
}

// newRestoreRun reads the config and the password: what can be judged before
// the repository or the target is touched.
func (p Plugin) newRestoreRun(ctx context.Context, h sdk.RestoreHost, cfg sdk.Config, req sdk.RestoreRequest) (*restoreRun, *sdk.RestoreError) {
	settings, err := parseRestore(cfg)
	if err != nil {
		// The code that writes enforces the rules of a plan itself (ADR 0008).
		return nil, refuse(ReasonTargetInvalid, stageCheck, "the restore plan is invalid: %s", err)
	}
	to := settings.to
	password, err := readPassword(h, to.PasswordRef)
	if err != nil {
		return nil, refuse(ReasonSecretInvalid, stageCheck, "%s", err)
	}
	return &restoreRun{
		p: p, ctx: ctx, h: h, req: req, database: settings.database, globals: settings.globals,
		conn: conn{p: p, log: h, to: to, password: password}, entry: newEntry(req.CommandID, to),
	}, nil
}

// readPassword is the content of the secret without its line ending, neither
// empty nor holding a NUL byte (F1 ПГ3).
func readPassword(h sdk.Host, name string) ([]byte, error) {
	value, err := h.Secret(name)
	if err != nil {
		return nil, err
	}
	return checkPassword(name, value)
}

// interrupted is the failure of a step whose context ended, at stage.
func (a *restoreRun) interrupted(stage string) *sdk.RestoreError {
	return &sdk.RestoreError{Reason: sdk.ReasonInterrupted, Stage: stage, Detail: "the command was interrupted", Err: context.Cause(a.ctx)}
}

// fail is a failure at stage; an ended context outranks it.
func (a *restoreRun) fail(reason, stage, format string, args ...any) *sdk.RestoreError {
	if a.ctx.Err() != nil {
		return a.interrupted(stage)
	}
	return refuse(reason, stage, format, args...)
}

// prepare runs the checks of ВК22 in order; the first that fails names the
// reason. Nothing is changed on the target.
func (a *restoreRun) prepare() *sdk.RestoreError {
	for _, check := range []func() *sdk.RestoreError{
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
func (a *restoreRun) checkSnapshot() *sdk.RestoreError {
	list, err := a.h.Snapshots(a.ctx)
	if err != nil {
		return staged(err, stageCheck)
	}
	snap, mine, f := chooseSnapshot(list, a.req)
	if f != nil {
		return f
	}
	return a.planSnapshot(snap, mine)
}

// planSnapshot lays out the snapshot, which must be one of a database, and
// keeps the layout for the steps that follow.
func (a *restoreRun) planSnapshot(snap sdk.SnapshotInfo, mine []sdk.SnapshotInfo) *sdk.RestoreError {
	if tagValue(snap.Tags, partTag) != partDatabase {
		return a.fail(sdk.ReasonResticOutputUnexpected, stageCheck, "snapshot %s is not the snapshot of a PostgreSQL database (tag postgresql.part=database): choose the snapshot of the database", shortID(snap.ID))
	}
	l, f := planLayout(mine, snap)
	if f != nil {
		return f
	}
	a.snap, a.layout = snap, l
	if a.globals && l.globals == nil {
		return a.fail(ReasonGlobalsSnapshotMissing, stageCheck, "snapshot %s has no snapshot of global objects (postgresql.main_snapshot=%s): restore without restore_globals, or back the source up with include_globals", shortID(snap.ID), snap.ID)
	}
	return nil
}
