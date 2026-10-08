// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// lockWaitMillis bounds the wait of pg_dump for a table lock to 5 minutes
// (F1 ПГ9).
const lockWaitMillis = 300000

// Stream implements sdk.Plugin: pg_dump writes the archive to w. It returns
// nil only when pg_dump exited with 0 and the whole non-empty archive, which
// starts with the signature of the custom format, reached w (F1 ПГ10).
func (p Plugin) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, _ sdk.Dump, w io.Writer) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	password, t, err := p.setup(h, c)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &archive{w: w, stop: cancel}
	o := p.run(runCtx, h, t.pgDump, c.pgDumpArgs(), p.childEnv(password), out)
	return out.verdict(ctx, o)
}

// pgDumpArgs are the arguments of pg_dump (F1 ПГ9).
func (c config) pgDumpArgs() []string {
	args := []string{"--format=custom", "--compress=0", "--no-password", fmt.Sprintf("--lock-wait-timeout=%d", lockWaitMillis)}
	for _, s := range c.ExcludeSchemas {
		args = append(args, "--exclude-schema="+s)
	}
	for _, t := range c.ExcludeTables {
		args = append(args, "--exclude-table="+t)
	}
	return append(args, "--dbname="+c.conninfo())
}

// archive passes the output of pg_dump to w once its first bytes prove it is
// an archive of the custom format, and stops pg_dump when that is not so or
// w fails.
type archive struct {
	w    io.Writer
	stop func()

	head       []byte // the first bytes, until the signature is checked
	checked    bool
	written    bool // pg_dump wrote at least a byte
	writeErr   error
	notArchive bool
}

func (a *archive) Write(p []byte) (int, error) {
	if len(p) > 0 {
		a.written = true
	}
	if !a.checked {
		a.head = append(a.head, p...)
		if len(a.head) < len(archiveSignature) {
			return len(p), nil
		}
		if string(a.head[:len(archiveSignature)]) != archiveSignature {
			a.notArchive = true
			a.stop()
			return 0, errors.New("not a custom-format archive")
		}
		a.checked = true
		head := a.head
		a.head = nil
		return len(p), a.forward(head)
	}
	return len(p), a.forward(p)
}

// forward writes to w; a failure of w stops pg_dump.
func (a *archive) forward(p []byte) error {
	if _, err := a.w.Write(p); err != nil {
		a.writeErr = err
		a.stop()
		return err
	}
	return nil
}

// verdict is the error of the stream, nil when the archive is complete; ctx
// is the context of the step, not the one that stopped pg_dump.
func (a *archive) verdict(ctx context.Context, o outcome) error {
	if err := a.interrupted(ctx); err != nil {
		return err
	}
	if err := o.failure(); err != nil {
		return err
	}
	return a.complete()
}

// interrupted is why the stream was stopped, if it was.
func (a *archive) interrupted(ctx context.Context) error {
	switch {
	case a.writeErr != nil:
		return fmt.Errorf("cannot pass the dump to restic: %w", a.writeErr)
	case a.notArchive:
		return errors.New("pg_dump failed: the output is not a custom-format archive")
	case ctx.Err() != nil:
		return context.Cause(ctx)
	}
	return nil
}

// complete checks what pg_dump wrote when it exited with 0.
func (a *archive) complete() error {
	switch {
	case !a.written:
		return errors.New("pg_dump failed: empty output")
	case !a.checked:
		return errors.New("pg_dump failed: the output is not a custom-format archive")
	}
	return nil
}
