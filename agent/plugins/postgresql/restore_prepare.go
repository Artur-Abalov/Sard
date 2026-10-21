// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

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
	if f == nil && len(existing) > 0 {
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
	capacity, err := strconv.ParseUint(o.last(), 10, 64)
	if !o.ok() || err != nil {
		return a.fail(ReasonConnect, stageCheck, "cannot read the size of the lock table of the target server: %s", o.problem())
	}
	share := a.p.lockShare()
	a.single = float64(objects) < share*float64(capacity)
	mode := "separate transactions"
	if a.single {
		mode = "single transaction"
	}
	a.h.Log(sdk.LevelInfo, fmt.Sprintf("pg_restore mode: %s (%d objects, lock capacity %d, share %s)", mode, objects, capacity, strconv.FormatFloat(share, 'g', -1, 64)))
	return nil
}

// countObjects is the number of entries of the table of contents of the dump:
// its head goes to pg_restore -l (the table lies at the start of an archive of
// the custom format), and restic dump is stopped.
func (a *restoreRun) countObjects() (int, *sdk.RestoreError) {
	res := a.stream([]string{"-l"}, true, nil)
	if f := a.streamFailure(res, stageCheck, "cannot read the table of contents of the dump"); f != nil {
		return 0, f
	}
	n := 0
	for _, line := range res.o.lines() {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, ";") {
			n++
		}
	}
	return n, nil
}

// streamResult is a run of pg_restore fed by restic dump.
type streamResult struct {
	o outcome
	// dumpErr is why restic dump failed, nil if it did not. It is not set for a
	// dump the plugin stopped or whose pipe pg_restore closed.
	dumpErr error
}

// errConsumerGone is what restic dump meets when pg_restore stopped reading.
var errConsumerGone = errors.New("pg_restore stopped reading")

// stream runs pg_restore with args and feeds it the file of the dump from
// restic. A reader that has what it needs (the table of contents) stops the
// dump; for a restore the dump ends by itself.
func (a *restoreRun) stream(args []string, stopDump bool, onWrite func(total uint64)) streamResult {
	file := a.layout.dump
	pr, pw := io.Pipe()
	w := &countingWriter{w: pw, onWrite: onWrite}
	dctx, stop := context.WithCancel(a.ctx)
	defer stop()
	dump := &dumpRun{done: make(chan struct{})}
	go func() {
		// The failure is kept before the pipe is closed with it, so that a
		// pg_restore that fails on the broken input finds it there.
		dump.finish(a.h.Dump(dctx, file.snapshotID, file.path, w), pw)
	}()
	o := a.conn.run(a.ctx, a.conn.tools.pgRestore, args, pr)
	_ = pr.CloseWithError(errConsumerGone)
	if !dump.finished() && (stopDump || !o.ok()) {
		stop()
		<-dump.done
		return streamResult{o: o}
	}
	<-dump.done
	if w.failed() {
		return streamResult{o: o} // the pipe was closed by pg_restore: its own failure comes first
	}
	return streamResult{o: o, dumpErr: dump.err}
}

// dumpRun is a restic dump running beside pg_restore.
type dumpRun struct {
	mu   sync.Mutex
	err  error
	ok   bool
	done chan struct{}
}

func (d *dumpRun) finish(err error, pw *io.PipeWriter) {
	d.mu.Lock()
	d.err, d.ok = err, true
	d.mu.Unlock()
	_ = pw.CloseWithError(err)
	close(d.done)
}

func (d *dumpRun) finished() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ok
}

// streamFailure is why the run failed, nil if it did not: the end of the
// context, restic dump failing (the storage), or pg_restore.
func (a *restoreRun) streamFailure(res streamResult, stage, what string) *sdk.RestoreError {
	switch {
	case a.ctx.Err() != nil:
		return a.interrupted(stage)
	case res.dumpErr != nil:
		return staged(res.dumpErr, stage)
	case !res.o.ok():
		return refuse(ReasonRestoreFailed, stage, "%s: %s", what, res.o.text())
	}
	return nil
}

// countingWriter counts what restic dump writes into the pipe, and remembers a
// failed write.
type countingWriter struct {
	w       io.Writer
	onWrite func(total uint64)
	total   uint64
	err     error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.total += uint64(n)
	if err != nil {
		c.err = err
	}
	if c.onWrite != nil {
		c.onWrite(c.total)
	}
	return n, err
}

func (c *countingWriter) failed() bool { return c.err != nil }
