// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// direct is the plugin with the fake tools, called without the agent: the
// errors of its methods are seen as they are, not as the status of a step.
type direct struct {
	plugin postgresql.Plugin
	proc   *fakeRunner
	host   *host
}

func newDirect() *direct {
	files := &fakeFS{nodes: map[string]fs.FileMode{"/usr/bin": fs.ModeDir | 0o755}}
	files.install("/usr/bin", "pg_dump", "psql", "pg_dumpall")
	proc := newRunner(func() agentv1.StepPhase { return agentv1.StepPhase_STEP_PHASE_UNSPECIFIED })
	env := func() []string { return []string{"PATH=/usr/bin"} }
	return &direct{plugin: postgresql.Plugin{Runner: proc, FS: files, Environ: env}, proc: proc, host: &host{}}
}

func cfgOf(cfg map[string]any) sdk.Config { return sdk.Config(js(cfg)) }

// A config that is not JSON is the error of every phase, nothing is started.
func TestDirectPhasesRejectAConfigThatIsNotJSON(t *testing.T) {
	d := newDirect()
	ctx := context.Background()
	bad := sdk.Config("not json")
	if err := d.plugin.Prepare(ctx, d.host, bad); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("Prepare: %v", err)
	}
	if _, err := d.plugin.Dump(ctx, d.host, bad); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("Dump: %v", err)
	}
	if err := d.plugin.Stream(ctx, d.host, bad, sdk.Dump{}, &bytes.Buffer{}); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("Stream: %v", err)
	}
	if d.proc.started() != 0 {
		t.Errorf("processes started: %d", d.proc.started())
	}
}

// A step whose context is done while pg_dumpall runs returns the cause of the
// context, not the exit of the stopped process.
func TestPgDumpallStoppedByTheContextReturnsItsCause(t *testing.T) {
	d := newDirect()
	cause := errors.New("the step timed out")
	ctx, cancel := context.WithCancelCause(context.Background())
	d.proc.on("pg_dumpall", func(c context.Context, _ plugCmd) (int, error) {
		cancel(cause)
		<-c.Done()
		return -1, errTerminated
	})
	cfg := cfgOf(kg())
	if err := d.plugin.Prepare(ctx, d.host, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := d.plugin.Dump(ctx, d.host, cfg); !errors.Is(err, cause) {
		t.Errorf("Dump: %v", err)
	}
}

// The same for pg_dump while it streams.
func TestPgDumpStoppedByTheContextReturnsItsCause(t *testing.T) {
	d := newDirect()
	cause := errors.New("the step was cancelled")
	ctx, cancel := context.WithCancelCause(context.Background())
	d.proc.on("pg_dump", func(c context.Context, cmd plugCmd) (int, error) {
		_, _ = cmd.Stdout.Write([]byte(archive(100)))
		cancel(cause)
		<-c.Done()
		return -1, errTerminated
	})
	cfg := cfgOf(k())
	if err := d.plugin.Prepare(ctx, d.host, cfg); err != nil {
		t.Fatal(err)
	}
	dump, err := d.plugin.Dump(ctx, d.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.plugin.Stream(ctx, d.host, cfg, dump, &bytes.Buffer{}); !errors.Is(err, cause) {
		t.Errorf("Stream: %v", err)
	}
}

// psql stopped by the context returns the cause too.
func TestPsqlStoppedByTheContextReturnsItsCause(t *testing.T) {
	d := newDirect()
	cause := errors.New("cancelled during preparation")
	ctx, cancel := context.WithCancelCause(context.Background())
	d.proc.on("psql", func(c context.Context, _ plugCmd) (int, error) {
		cancel(cause)
		<-c.Done()
		return -1, errTerminated
	})
	if err := d.plugin.Prepare(ctx, d.host, cfgOf(k())); !errors.Is(err, cause) {
		t.Errorf("Prepare: %v", err)
	}
}

// The tags of Dump are the versions Prepare learned, the names are encoded.
func TestDumpCarriesWhatPrepareLearned(t *testing.T) {
	d := newDirect()
	d.proc.on("psql", say(psqlRow(160004, false, true, "backup"), nil, 0))
	cfg := cfgOf(kg(o{"database": "my db"}))
	dump := d.dumpOf(t, cfg)
	want := map[string]string{"server_version": "16.4", "pg_dump_version": "18.0", "database": "my%20db", "format": "custom", "part": "database"}
	if dump.Filename != "my%20db.dump" || !maps.Equal(dump.Tags, want) {
		t.Errorf("Dump = %+v", dump)
	}
	if len(dump.Extra) != 1 || dump.Extra[0].Name != "my%20db.globals.sql" || dump.Extra[0].Tags["pg_dump_version"] != "18.0" {
		t.Errorf("Extra = %+v", dump.Extra)
	}
	// What Prepare learned is used once: a second Dump is of no step.
	if _, err := d.plugin.Dump(context.Background(), d.host, cfg); err == nil {
		t.Error("a second Dump of one Prepare succeeded")
	}
}

// dumpOf prepares and dumps a step of cfg.
func (d *direct) dumpOf(t *testing.T, cfg sdk.Config) sdk.Dump {
	t.Helper()
	ctx := context.Background()
	if err := d.plugin.Prepare(ctx, d.host, cfg); err != nil {
		t.Fatal(err)
	}
	dump, err := d.plugin.Dump(ctx, d.host, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return dump
}
