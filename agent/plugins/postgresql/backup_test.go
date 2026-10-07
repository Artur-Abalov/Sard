// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Scenario: Версии разбираются из ответа pg_dump и сервера.
func TestVersionsAreReadFromTheAnswersOfPgDumpAndTheServer(t *testing.T) {
	cases := []struct {
		pgDump string
		num    int
		tool   string
		server string
	}{
		{"pg_dump (PostgreSQL) 18.0", 180000, "18.0", "18.0"},
		{"pg_dump (PostgreSQL) 17.2 (Ubuntu 17.2-1.pgdg24.04+1)", 160004, "17.2", "16.4"},
		{"pg_dump (PostgreSQL) 18beta1", 140013, "18beta1", "14.13"},
		{"pg_dump (PostgreSQL) 18rc1", 180000, "18rc1", "18.0"},
		{"pg_dump (PostgreSQL) 14.2", 149999, "14.2", "14.9999"},
	}
	for _, c := range cases {
		t.Run(c.tool+" "+c.server, func(t *testing.T) {
			r := newRig(t)
			r.proc.on("pg_dump --version", say(c.pgDump+"\n", nil, 0))
			r.proc.on("psql", say(psqlRow(c.num, false, true, "backup"), nil, 0))
			want(t, r.backup(k()), succeeded)
			tags := tagsOf(r.restic.backups()[0])
			for _, w := range []string{"postgresql.pg_dump_version=" + c.tool, "postgresql.server_version=" + c.server} {
				if !slices.Contains(tags, w) {
					t.Errorf("tags %q lack %s", tags, w)
				}
			}
		})
	}
}

// Scenario: pg_dump запускается в формате custom без сжатия и с ограничением ожидания блокировок.
func TestPgDumpRunsInTheCustomFormatWithoutCompression(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k()), succeeded)
	args := r.proc.ran("pg_dump")[0].Args
	for _, a := range []string{"--format=custom", "--compress=0", "--no-password", "--lock-wait-timeout=300000"} {
		if !slices.Contains(args, a) {
			t.Errorf("args %q lack %s", args, a)
		}
	}
	b := r.restic.backups()[0]
	if !slices.Contains(b.Args, "--stdin") || flag(b, "--stdin-filename") != "app.dump" {
		t.Errorf("restic args = %q", b.Args)
	}
}

// Scenario: Исключения передаются pg_dump по одному слитно с флагом.
func TestExcludesGoToPgDumpOneByOneJoinedToTheFlag(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k(o{"exclude_schemas": []string{"audit", "-x"}, "exclude_tables": []string{"public.log_*"}})), succeeded)
	var got []string
	for _, a := range r.proc.ran("pg_dump")[0].Args {
		if strings.HasPrefix(a, "--exclude-") {
			got = append(got, a)
		}
	}
	if want := []string{"--exclude-schema=audit", "--exclude-schema=-x", "--exclude-table=public.log_*"}; !slices.Equal(got, want) {
		t.Errorf("excludes = %q, want %q", got, want)
	}
}

// Scenario: Имя файла дампа в снимке строится из имени базы.
func TestNameOfTheDumpFileIsBuiltFromTheDatabaseName(t *testing.T) {
	cases := map[string]string{
		"app":     "app.dump",
		"My_DB-2": "My_DB-2.dump",
		"my db":   "my%20db.dump",
		"a.b":     "a%2Eb.dump",
		"..":      "%2E%2E.dump",
		"x,y":     "x%2Cy.dump",
		"склад":   "%D1%81%D0%BA%D0%BB%D0%B0%D0%B4.dump",
	}
	for db, file := range cases {
		r := newRig(t)
		want(t, r.backup(k(o{"database": db})), succeeded)
		if got := flag(r.restic.backups()[0], "--stdin-filename"); got != file {
			t.Errorf("database %q: file %q, want %q", db, got, file)
		}
	}
}

// Scenario: Метка шага с ключом метки плагина проваливает шаг до restic.
func TestStepTagWithTheKeyOfAPluginTagFailsBeforeRestic(t *testing.T) {
	r := newRig(t)
	s := step(js(k()))
	s.Tags["postgresql.format"] = "x"
	res := r.run(s)
	want(t, res, failed)
	mentions(t, res.GetMessage(), "postgresql.format")
	if len(r.restic.run) != 0 {
		t.Errorf("restic ran %d times", len(r.restic.run))
	}
}

// Scenario: Счётчик байт в загрузке не убывает.
func TestByteCounterOfTheUploadDoesNotDecrease(t *testing.T) {
	r := newRig(t)
	r.restic.bytesDone = []uint64{100, 400, 1024}
	want(t, r.backup(k()), succeeded)
	got := r.sink.bytesIn(uploading)
	if !slices.Equal(got, []uint64{0, 100, 400, 1024}) {
		t.Errorf("bytes_processed = %v", got)
	}
}

// Scenario: Предупреждения pg_dump попадают в лог шага и не проваливают его.
func TestWarningsOfPgDumpGoToTheLogAndDoNotFailTheStep(t *testing.T) {
	const w = "pg_dump: warning: there are circular foreign-key constraints"
	r := newRig(t)
	r.proc.on("pg_dump", func(ctx context.Context, c plugCmd) (int, error) {
		c.Stderr(w)
		c.Stderr("pg_dump: dumping contents of table \"public.t\"")
		return say(archive(100), nil, 0)(ctx, c)
	})
	want(t, r.backup(k()), succeeded)
	if got := r.sink.warnings(); !slices.Contains(got, w) {
		t.Errorf("warnings = %q", got)
	}
	var info bool
	for _, l := range r.sink.lines() {
		if l.Level == agentv1.LogLevel_LOG_LEVEL_INFO && strings.Contains(l.Text, "dumping contents") {
			info = true
		}
	}
	if !info {
		t.Errorf("the line without a warning is not INFO: %+v", r.sink.lines())
	}
}

// Scenario: Сбой pg_dump проваливает шаг и не оставляет снимка.
func TestPgDumpFailureFailsTheStepAndLeavesNoSnapshot(t *testing.T) {
	big := func(c plugCmd) {
		_, _ = io.WriteString(c.Stdout, archive(10<<20))
	}
	cases := []struct {
		name   string
		run    behaviour
		reason string
	}{
		{"exit 1", func(_ context.Context, c plugCmd) (int, error) {
			big(c)
			c.Stderr("pg_dump: error: connection lost")
			return 1, nil
		}, "connection lost"},
		{"killed", func(_ context.Context, c plugCmd) (int, error) {
			big(c)
			return -1, fmt.Errorf("signal: killed")
		}, "signal: killed"},
		{"no output", say("", nil, 0), "empty output"},
		{"plain SQL", say("-- plain SQL\n", nil, 0), "not a custom-format archive"},
		{"short, not an archive", say("PGDM", nil, 0), "not a custom-format archive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.proc.on("pg_dump", c.run)
			res := r.backup(k())
			want(t, res, failed)
			mentions(t, res.GetMessage(), "pg_dump", c.reason)
			oneLine(t, res.GetMessage())
			if len(r.restic.stdin) != 0 {
				t.Errorf("snapshots stored: %d", len(r.restic.stdin))
			}
			noOutput(t, res)
		})
	}
}

// A plain SQL dump never reaches restic: only an archive does.
func TestOnlyAnArchiveReachesRestic(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump", say("-- plain SQL, long enough\n", nil, 0))
	want(t, r.backup(k()), failed)
	if len(r.restic.stdin) != 0 {
		t.Errorf("restic stored %q", r.restic.stdin)
	}
}

// Scenario: Сбой репозитория во время дампа останавливает pg_dump.
func TestRepositoryFailureDuringTheDumpStopsPgDump(t *testing.T) {
	r := newRig(t)
	r.restic.backup = func(_ context.Context, _ int, cmd resticCommand) int {
		cmd.Stderr([]byte("Fatal: unable to save snapshot: storage is full"))
		return 1
	}
	stopped := make(chan struct{})
	r.proc.on("pg_dump", func(ctx context.Context, c plugCmd) (int, error) {
		defer close(stopped)
		chunk := []byte(archive(64 << 10))
		for ctx.Err() == nil {
			if _, err := c.Stdout.Write(chunk); err != nil {
				return -1, errTerminated
			}
		}
		return -1, errTerminated
	})
	res := r.backup(k())
	want(t, res, failed)
	mentions(t, res.GetMessage(), `repository "R"`, "storage is full")
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("pg_dump was not stopped")
	}
	noOutput(t, res)
}

// Scenario: Остановка во время загрузки: отмена и таймаут.
func TestCancelAndTimeoutStopPgDumpWhileUploading(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		status  agentv1.StepStatus
	}{{"cancel", 0, cancelled}, {"timeout", 50 * time.Millisecond, timedOut}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			stopped := make(chan struct{})
			r.proc.on("pg_dump", func(ctx context.Context, cmd plugCmd) (int, error) {
				defer close(stopped)
				_, _ = io.WriteString(cmd.Stdout, archive(1<<20))
				<-ctx.Done()
				return -1, errTerminated
			})
			s := step(js(k()))
			if c.timeout > 0 {
				s = withTimeout(s, c.timeout)
			}
			r.exec.Submit(s)
			if c.timeout == 0 {
				waitFor(t, "pg_dump", func() bool { return len(r.proc.ran("pg_dump")) == 1 })
				r.exec.Cancel("c1")
			}
			res := r.result()
			want(t, res, c.status)
			<-stopped
			if len(r.restic.stdin) != 0 {
				t.Errorf("snapshots stored: %d", len(r.restic.stdin))
			}
		})
	}
}
