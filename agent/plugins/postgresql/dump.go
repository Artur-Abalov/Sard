// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// maxGlobalsBytes is the largest output of pg_dumpall the plugin keeps in
// memory (F1 ПГ17d).
const maxGlobalsBytes = 64 << 20

// archiveSignature starts every pg_dump archive of the custom format.
const archiveSignature = "PGDMP"

// Dump implements sdk.Plugin: the dump of the database is streamed (see
// Stream); the global objects are dumped now, into memory, and stored as an
// extra snapshot after the dump of the database (F1 ПГ14, ПГ17).
func (p Plugin) Dump(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	c, err := parse(cfg)
	if err != nil {
		return sdk.Dump{}, err
	}
	v, ok := prepared.LoadAndDelete(h)
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
			"part":            "database",
		},
	}
	if !c.globals() {
		return d, nil
	}
	globals, err := p.globals(ctx, h, c, done)
	if err != nil {
		return sdk.Dump{}, err
	}
	d.Extra = []sdk.ExtraFile{globals}
	return d, nil
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
	out := &limited{max: maxGlobalsBytes, stop: cancel}
	o := p.run(runCtx, h, t.pgDumpall, args, p.childEnv(password), out)
	if err := out.verdict(ctx, o); err != nil {
		return sdk.ExtraFile{}, err
	}
	name := encodeName(c.Database)
	return sdk.ExtraFile{Name: name + ".globals.sql", Content: out.buf.Bytes(), Tags: map[string]string{
		"database":        name,
		"server_version":  done.server.text,
		"pg_dump_version": done.pgDumpall.text,
		"format":          "plain",
		"part":            "globals",
		"role_passwords":  fmt.Sprint(c.GlobalsRolePasswords),
	}}, nil
}

// limited keeps what is written to it, up to max bytes; more stops the
// process that writes.
type limited struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
	stop     func()
}

func (l *limited) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		l.exceeded = true
		l.stop()
		return 0, errors.New("output too large")
	}
	return l.buf.Write(p)
}

// verdict is the error of the run of pg_dumpall, nil when it left output; ctx
// is the context of the step, not the one that stopped pg_dumpall.
func (l *limited) verdict(ctx context.Context, o outcome) error {
	switch {
	case l.exceeded:
		return fmt.Errorf("pg_dumpall failed: the output is larger than %d MiB", l.max>>20)
	case ctx.Err() != nil:
		return context.Cause(ctx)
	}
	if err := o.failure(); err != nil {
		return err
	}
	if l.buf.Len() == 0 {
		return errors.New("pg_dumpall failed: empty output")
	}
	return nil
}
