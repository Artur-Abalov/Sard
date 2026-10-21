// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

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
