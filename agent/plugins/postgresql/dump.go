// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// maxGlobalsBytes is the largest file of global objects the plugin keeps in
// memory, when it dumps them (F1 ПГ17d) and when it restores them.
const maxGlobalsBytes = 64 << 20

// archiveSignature starts every pg_dump archive of the custom format.
const archiveSignature = "PGDMP"

// Dump implements sdk.Plugin: the dump of the database is streamed (see
// Stream); the global objects are dumped now, into memory, and stored as an
// extra snapshot after the dump of the database (F1 ПГ14, ПГ17). Then the
// transaction of the fingerprint is opened: its snapshot is exported for
// pg_dump and the fingerprint taken in it (V2); Release ends it.
func (p Plugin) Dump(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	// First, so that no failure below leaves the state of the step behind.
	v, ok := prepared.LoadAndDelete(h)
	c, err := parse(cfg)
	if err != nil {
		return sdk.Dump{}, err
	}
	if !ok {
		return sdk.Dump{}, errNotPrepared
	}
	done := v.(checked)
	name := encodeName(c.Database)
	d := sdk.Dump{
		Filename: name + ".dump",
		Tags: map[string]string{
			"server_version":  done.server.text,
			"pg_dump_version": done.pgDump.text,
			"database":        name,
			"format":          "custom",
			"part":            partDatabase,
		},
	}
	if done.systemID != "" {
		d.Tags["system_identifier"] = done.systemID
	}
	if d.Extra, err = p.extra(ctx, h, c, done); err != nil {
		return sdk.Dump{}, err
	}
	snap, fp, err := p.export(ctx, h, c)
	if err != nil {
		return sdk.Dump{}, err
	}
	snapshots.Store(h, snap)
	d.Fingerprint = &fp
	d.Release = func() {
		snapshots.Delete(h)
		snap.session.close()
	}
	return d, nil
}

// extra is the file of the global objects, when the config asks for it.
func (p Plugin) extra(ctx context.Context, h sdk.Host, c config, done checked) ([]sdk.ExtraFile, error) {
	if !c.globals() {
		return nil, nil
	}
	globals, err := p.globals(ctx, h, c, done)
	if err != nil {
		return nil, err
	}
	return []sdk.ExtraFile{globals}, nil
}

// exported is the transaction of the fingerprint of a step and the name of
// the snapshot it exported for pg_dump.
type exported struct {
	session *session
	name    string
}

// snapshots holds the exported snapshot of a step from Dump to Release.
var snapshots sync.Map // sdk.Host → *exported

var errNoSnapshot = errors.New("pg_dump: no snapshot of the fingerprint was exported for this step")

// export opens the transaction, exports its snapshot and takes the
// fingerprint in it; a failure ends the transaction and the backup.
func (p Plugin) export(ctx context.Context, h sdk.Host, c config) (*exported, sdk.Fingerprint, error) {
	password, t, err := p.setup(h, c)
	if err != nil {
		return nil, sdk.Fingerprint{}, err
	}
	s, err := p.openSession(ctx, h, t.psql, c.conninfo(), password)
	if err != nil {
		return nil, sdk.Fingerprint{}, err
	}
	snap, fp, err := p.exportIn(ctx, h, s, c)
	if err != nil {
		s.close()
		return nil, sdk.Fingerprint{}, err
	}
	return snap, fp, nil
}

// exportIn exports the snapshot of the session and takes the fingerprint in it.
func (p Plugin) exportIn(ctx context.Context, h sdk.Host, s *session, c config) (*exported, sdk.Fingerprint, error) {
	name, ok, err := s.ask(ctx, "SELECT 'R' || pg_catalog.pg_export_snapshot();")
	if err != nil {
		return nil, sdk.Fingerprint{}, err
	}
	if !ok {
		return nil, sdk.Fingerprint{}, errors.New("fingerprint: the snapshot could not be exported (see the log of the step)")
	}
	fp, err := p.measure(ctx, h, s, c, nil)
	return &exported{session: s, name: name}, fp, err
}

// globals runs pg_dumpall --globals-only into memory.
func (p Plugin) globals(ctx context.Context, h sdk.Host, c config, done checked) (sdk.ExtraFile, error) {
	password, t, err := p.setup(h, c)
	if err != nil {
		return sdk.ExtraFile{}, err
	}
	if t, err = p.locateDumpall(t); err != nil {
		return sdk.ExtraFile{}, err
	}
	args := []string{"--globals-only", "--no-password", "--dbname=" + c.conninfo()}
	if !c.GlobalsRolePasswords {
		args = append(args, "--no-role-passwords")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &limitedBuffer{max: maxGlobalsBytes, stop: cancel}
	o := p.run(runCtx, h, call{path: t.pgDumpall, args: args, environ: p.childEnv(password), stdout: out})
	if err := verdict(ctx, out, o); err != nil {
		return sdk.ExtraFile{}, err
	}
	name := encodeName(c.Database)
	return sdk.ExtraFile{Name: name + ".globals.sql", Content: out.Bytes(), Tags: map[string]string{
		"database":        name,
		"server_version":  done.server.text,
		"pg_dump_version": done.pgDumpall.text,
		"format":          "plain",
		"part":            partGlobals,
		"role_passwords":  fmt.Sprint(c.GlobalsRolePasswords),
	}}, nil
}

// limitedBuffer keeps what is written to it, up to max bytes; more is refused
// and stops the process that writes, if stop is set.
type limitedBuffer struct {
	bytes.Buffer
	max      int
	exceeded bool
	stop     func()
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.Len()+len(p) > l.max {
		l.exceeded = true
		if l.stop != nil {
			l.stop()
		}
		return 0, errors.New("output too large")
	}
	return l.Buffer.Write(p)
}

// verdict is the error of the run of pg_dumpall, nil when it left output; ctx
// is the context of the step, not the one that stopped pg_dumpall.
func verdict(ctx context.Context, l *limitedBuffer, o outcome) error {
	switch {
	case l.exceeded:
		return fmt.Errorf("pg_dumpall failed: the output is larger than %d MiB", l.max>>20)
	case ctx.Err() != nil:
		return context.Cause(ctx)
	}
	if err := o.failure(); err != nil {
		return err
	}
	if l.Len() == 0 {
		return errors.New("pg_dumpall failed: empty output")
	}
	return nil
}
