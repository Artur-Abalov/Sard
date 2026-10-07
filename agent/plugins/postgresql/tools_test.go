// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// noDump is "Дамп не запускался": no pg_dump without --version, no
// pg_dumpall, no restic backup.
func (r *rig) noDump(t *testing.T) {
	t.Helper()
	if n := len(r.proc.ran("pg_dump")) + len(r.proc.ran("pg_dumpall")); n != 0 || len(r.restic.backups()) != 0 {
		t.Errorf("a dump started: %d tool runs, %d restic backups", n, len(r.restic.backups()))
	}
}

// lastPhase is "последняя фаза прогресса".
func (r *rig) lastPhase(t *testing.T, phase agentv1.StepPhase) {
	t.Helper()
	got := r.sink.phases()
	if len(got) == 0 || got[len(got)-1] != phase {
		t.Errorf("phases = %v, want the last to be %v", got, phase)
	}
}

// Scenario: Отсутствие pg_dump в PATH проваливает шаг с подсказкой о пакете.
func TestMissingPgDumpInPathFailsTheStepWithAHintAboutThePackage(t *testing.T) {
	r := newRig(t)
	delete(r.fs.nodes, "/usr/bin/pg_dump")
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dump not found", "postgresql-client", "postgresql", "pg_dump_path")
	oneLine(t, res.GetMessage())
	r.noDump(t)
	r.lastPhase(t, preparing)
	noOutput(t, res)
}

// Scenario: Негодный pg_dump_path проваливает шаг и называет путь.
func TestBadPgDumpPathFailsTheStepAndNamesThePath(t *testing.T) {
	const path = "/opt/pg/bin/pg_dump"
	cases := map[string]struct {
		node   *fs.FileMode
		reason string
	}{
		"missing":        {nil, "no such file or directory"},
		"not runnable":   {modeOf(0o644), "permission denied"},
		"is a directory": {modeOf(fs.ModeDir | 0o755), "is a directory"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			if c.node != nil {
				r.fs.nodes[path] = *c.node
			}
			res := r.backup(k(o{"pg_dump_path": path}))
			want(t, res, failed)
			mentions(t, res.GetMessage(), path, c.reason)
			r.noDump(t)
		})
	}
}

func modeOf(m fs.FileMode) *fs.FileMode { return &m }

// Scenario: Указанный pg_dump_path используется вместо pg_dump из PATH.
func TestPgDumpPathIsUsedInsteadOfPgDumpFromPath(t *testing.T) {
	r := newRig(t)
	r.fs.install("/opt/pg18/bin", "pg_dump", "psql")
	res := r.backup(k(o{"pg_dump_path": "/opt/pg18/bin/pg_dump"}))
	want(t, res, succeeded)
	for _, c := range r.proc.calls {
		if c.Path != "/opt/pg18/bin/psql" && c.Path != "/opt/pg18/bin/pg_dump" {
			t.Errorf("started %s", c.Path)
		}
	}
	if len(r.proc.ran("psql")) == 0 || len(r.proc.ran("pg_dump")) == 0 {
		t.Errorf("psql %d, pg_dump %d", len(r.proc.ran("psql")), len(r.proc.ran("pg_dump")))
	}
}

// Scenario: Отсутствие psql рядом с pg_dump проваливает шаг с подсказкой о пакете.
func TestMissingPsqlNextToPgDumpFailsTheStep(t *testing.T) {
	r := newRig(t)
	delete(r.fs.nodes, "/usr/bin/psql")
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "psql", "/usr/bin/psql", "postgresql-client", "postgresql")
	r.noDump(t)
}

// Scenario: Непонятный ответ pg_dump о версии проваливает шаг.
func TestUnintelligibleVersionOfPgDumpFailsTheStep(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump --version", say("something else\n", nil, 0))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "something else")
	r.noDump(t)
}

// Scenario: pg_dump старше сервера проваливает шаг до дампа и называет обе версии.
func TestPgDumpOlderThanTheServerFailsBeforeTheDump(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump --version", say("pg_dump (PostgreSQL) 16.4\n", nil, 0))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "16.4", "18.0", "install pg_dump 18 or newer", "pg_dump_path")
	oneLine(t, res.GetMessage())
	r.noDump(t)
	r.lastPhase(t, preparing)
}

// Scenario: pg_dump новее или той же старшей версии подходит.
func TestPgDumpOfTheSameOrANewerMajorVersionIsFine(t *testing.T) {
	for _, c := range []struct {
		pgDump string
		server int
	}{{"18.0", 140013}, {"16.2", 160004}, {"16.4", 160004}} {
		r := newRig(t)
		r.proc.on("pg_dump --version", say("pg_dump (PostgreSQL) "+c.pgDump+"\n", nil, 0))
		r.proc.on("psql", say(psqlRow(c.server, false, true, "backup"), nil, 0))
		want(t, r.backup(k()), succeeded)
		if len(r.proc.ran("pg_dump")) != 1 {
			t.Errorf("pg_dump %s, server %d: pg_dump ran %d times", c.pgDump, c.server, len(r.proc.ran("pg_dump")))
		}
	}
}

// Scenario: Сервер старше PostgreSQL 14 не поддерживается.
func TestServerOlderThanPostgreSQL14IsNotSupported(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(130016, false, true, "backup"), nil, 0))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "13.16", "14")
	r.noDump(t)
}

// The reason is the operating system's, whatever it is.
func TestPgDumpPathThatCannotBeLookedAtSaysWhy(t *testing.T) {
	const path = "/opt/pg/bin/pg_dump"
	cases := map[string]struct {
		err    error
		reason string
	}{
		"permission":      {&fs.PathError{Op: "stat", Path: path, Err: syscall.EACCES}, "permission denied"},
		"not a dir":       {&fs.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}, "no such file or directory"},
		"another errno":   {&fs.PathError{Op: "stat", Path: path, Err: syscall.EIO}, "input/output error"},
		"not a PathError": {errors.New("boom"), "boom"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.fs.errs = map[string]error{path: c.err}
			res := r.backup(k(o{"pg_dump_path": path}))
			want(t, res, failed)
			mentions(t, res.GetMessage(), path, c.reason)
			if strings.Contains(res.GetMessage(), "/opt/pg/bin/pg_dump: ") {
				t.Errorf("the message holds the path twice: %q", res.GetMessage())
			}
		})
	}
}

// Empty entries of PATH are skipped, the next one is searched.
func TestEmptyEntriesOfPathAreSkipped(t *testing.T) {
	r := newRig(t)
	r.env = []string{"PATH=::/usr/bin", "HOME=/var/lib/sard"}
	want(t, r.backup(k()), succeeded)
	if got := r.proc.ran("pg_dump")[0].Path; got != "/usr/bin/pg_dump" {
		t.Errorf("pg_dump = %q", got)
	}
}

// A tool that cannot say its version fails the step with its own words.
func TestToolThatFailsToSayItsVersionFailsTheStep(t *testing.T) {
	for _, key := range []string{"pg_dump --version", "pg_dumpall --version"} {
		r := newRig(t)
		r.proc.on(key, fail("error while loading shared libraries: libpq.so.5"))
		res := r.backup(kg())
		want(t, res, failed)
		mentions(t, res.GetMessage(), strings.TrimSuffix(key, " --version"), "libpq.so.5")
		r.noDump(t)
	}
}

// The quote of a very long line of stderr is cut, the message stays one line.
func TestLongErrorLineIsCutInTheMessage(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", fail("psql: error: "+strings.Repeat("x", 5000)))
	res := r.backup(k())
	want(t, res, failed)
	if len(res.GetMessage()) > 1000 {
		t.Errorf("the message has %d bytes", len(res.GetMessage()))
	}
	oneLine(t, res.GetMessage())
}

// QA step 18: a pg_dump that is too old is reported with both versions even
// when no pg_dumpall lies beside it; the global objects come after.
func TestOldPgDumpIsReportedBeforeAMissingPgDumpall(t *testing.T) {
	r := newRig(t)
	delete(r.fs.nodes, "/usr/bin/pg_dumpall")
	r.proc.on("pg_dump --version", say("pg_dump (PostgreSQL) 16.4\n", nil, 0))
	res := r.backup(kg())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dump 16.4", "18.0", "install pg_dump 18 or newer")
	r.noDump(t)
}

// The message of a failed tool is its own name, the exit code and its words.
func TestMessageOfAFailedToolIsExact(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump --version", fail("error while loading shared libraries: libpq.so.5"))
	res := r.backup(k())
	want(t, res, failed)
	const text = "prepare: pg_dump failed (exit code 1): error while loading shared libraries: libpq.so.5"
	if res.GetMessage() != text {
		t.Errorf("message = %q, want %q", res.GetMessage(), text)
	}
}

// Empty lines of stderr are not lines of the log.
func TestEmptyLinesOfStderrAreNotLogged(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump", func(ctx context.Context, c plugCmd) (int, error) {
		c.Stderr("")
		c.Stderr("   ")
		c.Stderr("pg_dump: dumping")
		return say(archive(100), nil, 0)(ctx, c)
	})
	want(t, r.backup(k()), succeeded)
	for _, l := range r.sink.lines() {
		if strings.TrimSpace(l.Text) == "" {
			t.Errorf("an empty line in the log: %+v", r.sink.lines())
		}
	}
}

// The quote of an error is cut above 512 bytes, with an ellipsis, and not below.
func TestQuoteOfAnErrorIsCutAboveTheLimit(t *testing.T) {
	for _, n := range []int{511, 512, 513} {
		line := "psql: error: " + strings.Repeat("y", n-len("psql: error: "))
		r := newRig(t)
		r.proc.on("psql", say("", []string{line}, 2))
		res := r.backup(k())
		want(t, res, failed)
		quote := strings.TrimPrefix(res.GetMessage(), "prepare: psql failed (exit code 2): ")
		wantQuote := line
		if n > 512 {
			wantQuote = line[:512] + "..."
		}
		if quote != wantQuote {
			t.Errorf("%d bytes: quote of %d bytes, want %d", n, len(quote), len(wantQuote))
		}
	}
}

// A tool that fails without a word is named by its exit code alone.
func TestToolThatFailsWithoutAWordIsNamedByItsExitCode(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say("", nil, 3))
	res := r.backup(k())
	want(t, res, failed)
	if res.GetMessage() != "prepare: psql failed (exit code 3)" {
		t.Errorf("message = %q", res.GetMessage())
	}
}

// The line that names the error is quoted wherever it is among the lines, and
// the lines that report an error or a warning are WARN, the others INFO.
func TestQuoteIsTheLineThatNamesTheErrorAndThoseLinesAreWarnings(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say("", []string{
		"psql: error: connection to server failed: FATAL:  password authentication failed",
		"DETAIL:  Connection matched pg_hba.conf line 1",
		"FATAL:  the second one",
		"HINT:  nothing to add",
		"psql: warning: something odd",
	}, 2))
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "FATAL:  the second one")
	levels := map[string]agentv1.LogLevel{}
	for _, l := range r.sink.lines() {
		levels[l.Text] = l.Level
	}
	warn, info := agentv1.LogLevel_LOG_LEVEL_WARN, agentv1.LogLevel_LOG_LEVEL_INFO
	for text, level := range map[string]agentv1.LogLevel{
		"psql: error: connection to server failed: FATAL:  password authentication failed": warn,
		"FATAL:  the second one":                         warn,
		"psql: warning: something odd":                   warn,
		"DETAIL:  Connection matched pg_hba.conf line 1": info,
		"HINT:  nothing to add":                          info,
	} {
		if levels[text] != level {
			t.Errorf("%q is logged at %v, want %v", text, levels[text], level)
		}
	}
	// A line with only FATAL: is an error line as well.
	only := newRig(t)
	only.proc.on("psql", say("", []string{"FATAL:  the database system is shutting down", "DETAIL:  after it"}, 2))
	mentions(t, only.backup(k()).GetMessage(), "the database system is shutting down")
}

// The agent's variables of libpq are dropped wherever they are in its environment.
func TestPGVariablesAreDroppedWhereverTheyAre(t *testing.T) {
	r := newRig(t)
	r.env = []string{"PGHOST=other", "PATH=/usr/bin", "PGSSLMODE=disable", "HOME=/var/lib/sard", "LC_ALL=ru_RU.UTF-8", "TZ=UTC"}
	want(t, r.backup(k()), succeeded)
	c := r.proc.ran("psql")[0]
	for _, kv := range []string{"PATH=/usr/bin", "HOME=/var/lib/sard", "TZ=UTC"} {
		if !slices.Contains(c.Env, kv) {
			t.Errorf("env %q lacks %s", c.Env, kv)
		}
	}
	for _, name := range []string{"PGHOST", "PGSSLMODE", "LC_ALL"} {
		if _, ok := c.env(name); ok {
			t.Errorf("%s reached psql", name)
		}
	}
}

// An empty entry of PATH is the current directory for a shell, and is not
// searched: a pg_dump there is not the one of the host's tools.
func TestEmptyEntryOfPathIsNotTheCurrentDirectory(t *testing.T) {
	r := newRig(t)
	r.fs.nodes["pg_dump"] = 0o755
	r.fs.nodes["psql"] = 0o755
	r.env = []string{"PATH=:/nonexistent", "HOME=/var/lib/sard"}
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dump not found")
}
