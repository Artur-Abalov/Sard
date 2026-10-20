// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// maxGlobalsBytes is the largest file of global objects the restore reads
// into memory (as the plugin does when it writes it, F1 ПГ17d).
const maxGlobalsBytes = 64 << 20

// rolesScript lists the roles the file creates that exist on the target, and
// the bootstrap superuser (OID 10), which the target always has.
const rolesScript = `select r.oid = 10, r.rolname from pg_roles r where r.oid = 10 or r.rolname = any(string_to_array(:'names', chr(1)));`

// checkGlobals reads the file of the global objects and judges it before any
// change (ВК26): no psql commands, no tablespaces, no role that exists.
func (a *pgAttempt) checkGlobals() *Failure {
	if !a.req.Plan.Globals {
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
		a.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, fmt.Sprintf("the statements about the role %q are skipped: it is the bootstrap superuser of the target cluster", bootstrap))
	}
	a.roles = slices.DeleteFunc(slices.Clone(g.roles), func(n string) bool { return n == bootstrap })
	return nil
}

// checkRoles: none of the roles the file creates exists on the target, except
// the bootstrap superuser, whose name it returns.
func (a *pgAttempt) checkRoles(names []string) (bootstrap string, f *Failure) {
	bootstrap, existing, f := a.existingRoles(names)
	if f == nil && len(existing) > 0 {
		f = a.fail(refusal.RolesExist, StageCheck,
			"the roles %s already exist on the target server and the restore does not change an existing role; restore without restore_globals", quoted(existing))
	}
	return bootstrap, f
}

// refuseGlobals: the file holds a command of psql, or creates a tablespace.
func (a *pgAttempt) refuseGlobals(g globals) *Failure {
	if g.meta != 0 {
		return a.fail(refusal.PGGlobalsFailed, StageCheck, "line %d of the file of the global objects is a command of psql, not SQL: the file is refused", g.meta)
	}
	if len(g.tablespaces) > 0 {
		return a.fail(refusal.GlobalsTablespaces, StageCheck,
			"the global objects create the tablespaces %s: a tablespace cannot be created in a transaction and lies in a directory of the source host; restore without restore_globals (the database then goes without tablespaces) or create them by hand", quoted(g.tablespaces))
	}
	return nil
}

// readGlobals reads the file of the global objects of the plan out of restic.
func (a *pgAttempt) readGlobals() (string, *Failure) {
	file := a.plan.Files[len(a.plan.Files)-1]
	buf := &limitedBuffer{max: maxGlobalsBytes}
	if err := a.repo.Dump(a.ctx, file.SnapshotID, file.Path, buf); err != nil {
		if a.ctx.Err() != nil {
			return "", a.interrupted(StageCheck)
		}
		if buf.exceeded {
			return "", &Failure{Reason: refusal.PGGlobalsFailed, Stage: StageCheck, Detail: fmt.Sprintf("the file of the global objects is larger than %d MiB", maxGlobalsBytes>>20)}
		}
		return "", asFailure(repoinit.FromRestic(a.ctx, err, a.target), StageCheck, a.target)
	}
	return buf.String(), nil
}

// limitedBuffer keeps what is written to it, up to max bytes.
type limitedBuffer struct {
	bytes.Buffer
	max      int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.exceeded = true
		return 0, errors.New("the file is too large")
	}
	return b.Buffer.Write(p)
}

// existingRoles asks the target which of the roles exist; it returns the name
// of the bootstrap superuser and the existing roles other than it.
func (a *pgAttempt) existingRoles(names []string) (bootstrap string, existing []string, f *Failure) {
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, rolesScript, "names", strings.Join(names, "\x01"))
	if !o.ok() {
		return "", nil, a.fail(refusal.PGConnect, StageCheck, "cannot read the roles of the target server: %s", o.text())
	}
	for _, line := range o.stdout {
		isBootstrap, name, ok := strings.Cut(line, "|")
		switch {
		case !ok:
			return "", nil, a.fail(refusal.PGConnect, StageCheck, "unexpected answer of psql: %q", line)
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
func (a *pgAttempt) checkNewName() *Failure {
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, existsScript, "dbname", a.name())
	switch {
	case !o.ok():
		return a.fail(refusal.PGConnect, StageCheck, "cannot look for the database %q: %s", a.name(), o.text())
	case o.last() == "t":
		return a.fail(refusal.DatabaseExists, StageCheck, "the database %q already exists on the target server: the restore never writes into an existing database; choose a new name", a.name())
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
func (a *pgAttempt) chooseMode() *Failure {
	objects, f := a.countObjects()
	if f != nil {
		return f
	}
	o := a.conn.psql(a.ctx, a.r, maintenanceDB, capacityScript)
	capacity, err := strconv.ParseUint(o.last(), 10, 64)
	if !o.ok() || err != nil {
		return a.fail(refusal.PGConnect, StageCheck, "cannot read the size of the lock table of the target server: %s", o.problem())
	}
	share := a.e.opts.Postgres.LockShare
	a.single = float64(objects) < share*float64(capacity)
	mode := "separate transactions"
	if a.single {
		mode = "single transaction"
	}
	a.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, fmt.Sprintf("pg_restore mode: %s (%d objects, lock capacity %d, share %s)", mode, objects, capacity, strconv.FormatFloat(share, 'g', -1, 64)))
	return nil
}

// countObjects is the number of entries of the table of contents of the dump:
// its head goes to pg_restore -l (the table lies at the start of an archive of
// the custom format), and restic dump is stopped.
func (a *pgAttempt) countObjects() (int, *Failure) {
	res := a.stream([]string{"-l"}, true, nil)
	if f := a.streamFailure(res, StageCheck, "cannot read the table of contents of the dump"); f != nil {
		return 0, f
	}
	n := 0
	for _, line := range res.o.stdout {
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
	// dump the agent stopped or whose pipe pg_restore closed.
	dumpErr error
}

// errConsumerGone is what restic dump meets when pg_restore stopped reading.
var errConsumerGone = errors.New("pg_restore stopped reading")

// stream runs pg_restore with args and feeds it the file of the dump from
// restic. A reader that has what it needs (the table of contents) stops the
// dump; for a restore the dump ends by itself.
func (a *pgAttempt) stream(args []string, stopDump bool, onWrite func(total uint64)) streamResult {
	file := a.plan.Files[0]
	pr, pw := io.Pipe()
	w := &countingWriter{w: pw, onWrite: onWrite}
	dctx, stop := context.WithCancel(a.ctx)
	defer stop()
	dump := &dumpRun{done: make(chan struct{})}
	go func() {
		// The failure is kept before the pipe is closed with it, so that a
		// pg_restore that fails on the broken input finds it there.
		dump.finish(a.repo.Dump(dctx, file.SnapshotID, file.Path, w), pw)
	}()
	o := a.conn.run(a.ctx, a.r, a.conn.tools.restore, args, pr)
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
func (a *pgAttempt) streamFailure(res streamResult, stage Stage, what string) *Failure {
	switch {
	case a.ctx.Err() != nil:
		return a.interrupted(stage)
	case res.dumpErr != nil:
		return asFailure(repoinit.FromRestic(a.ctx, res.dumpErr, a.target), stage, a.target)
	case !res.o.ok():
		return &Failure{Reason: refusal.PGRestoreFailed, Stage: stage, Detail: what + ": " + res.o.text()}
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
