// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

// Scenario: Бэкап по умолчанию даёт снимок базы и связанный снимок глобальных объектов.
func TestDefaultBackupGivesTheDatabaseSnapshotAndALinkedGlobalsSnapshot(t *testing.T) {
	r := newRig(t)
	res := r.backup(kg())
	want(t, res, succeeded)
	b := r.restic.backups()
	if len(b) != 2 || len(r.restic.stdin) != 2 {
		t.Fatalf("restic backups: %d, snapshots: %d", len(b), len(r.restic.stdin))
	}
	if res.GetBackup().GetSnapshotId() != snapshotID(0) || flag(b[0], "--stdin-filename") != "app.dump" {
		t.Errorf("main snapshot %q, file %q", res.GetBackup().GetSnapshotId(), flag(b[0], "--stdin-filename"))
	}
	if !slices.Contains(tagsOf(b[0]), "postgresql.part=database") {
		t.Errorf("tags of the main snapshot: %q", tagsOf(b[0]))
	}
	if flag(b[1], "--stdin-filename") != "app.globals.sql" || r.restic.stdin[1] != globalsSQL {
		t.Errorf("second snapshot: file %q, content %q", flag(b[1], "--stdin-filename"), r.restic.stdin[1])
	}
}

// Scenario: Снимок глобальных объектов получает метки шага, плагина и ссылку на снимок базы.
func TestGlobalsSnapshotGetsTheTagsOfTheStepAndThePluginAndALink(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(160004, false, true, "backup"), nil, 0))
	res := r.backup(kg())
	want(t, res, succeeded)
	want2 := []string{
		"postgresql.database=app", "postgresql.format=plain", "postgresql.main_snapshot=" + res.GetBackup().GetSnapshotId(),
		"postgresql.part=globals", "postgresql.pg_dump_version=18.0", "postgresql.role_passwords=false",
		"postgresql.server_version=16.4", "sard.run=r1", "sard.source=s1",
	}
	if got := tagsOf(r.restic.backups()[1]); !slices.Equal(got, want2) {
		t.Errorf("tags = %q\nwant   %q", got, want2)
	}
	main := []string{"postgresql.database=app", "postgresql.format=custom", "postgresql.part=database",
		"postgresql.pg_dump_version=18.0", "postgresql.server_version=16.4", "sard.run=r1", "sard.source=s1"}
	if got := tagsOf(r.restic.backups()[0]); !slices.Equal(got, main) {
		t.Errorf("tags of the main snapshot = %q\nwant %q", got, main)
	}
}

// Scenario: Объёмы результата — суммы по снимку базы и снимку глобальных объектов.
func TestVolumesOfTheResultAreSumsOverBothSnapshots(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump", say(archive(1000), nil, 0))
	res := r.backup(kg())
	want(t, res, succeeded)
	out := res.GetBackup()
	if out.GetTotalBytes() != uint64(1000+len(globalsSQL)) || out.GetAddedBytes() != uint64(1005+len(globalsSQL)+5) || out.GetRepositoryId() != repoID {
		t.Errorf("output = %v", out)
	}
}

// Scenario: Без include_globals pg_dumpall не ищется и не запускается.
func TestWithoutIncludeGlobalsPgDumpallIsNeitherLookedForNorStarted(t *testing.T) {
	r := newRig(t)
	delete(r.fs.nodes, "/usr/bin/pg_dumpall")
	want(t, r.backup(k()), succeeded)
	if len(r.proc.ran("pg_dumpall"))+len(r.proc.ran("pg_dumpall --version")) != 0 {
		t.Error("pg_dumpall was started")
	}
	if slices.Contains(r.fs.stats, "/usr/bin/pg_dumpall") || len(r.restic.backups()) != 1 {
		t.Errorf("stats %q, restic backups %d", r.fs.stats, len(r.restic.backups()))
	}
}

// Scenario: По умолчанию pg_dumpall запускается без паролей ролей.
func TestPgDumpallRunsWithoutRolePasswordsByDefault(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(kg()), succeeded)
	c := r.proc.ran("pg_dumpall")[0]
	for _, a := range []string{"--globals-only", "--no-role-passwords"} {
		if !slices.Contains(c.Args, a) {
			t.Errorf("args %q lack %s", c.Args, a)
		}
	}
	if c.arg("--dbname") != r.proc.ran("pg_dump")[0].arg("--dbname") {
		t.Errorf("connection strings differ: %q", c.Args)
	}
	if v, _ := c.env("PGPASSWORD"); v != P {
		t.Errorf("PGPASSWORD = %q", v)
	}
	for _, a := range c.Args {
		if strings.Contains(a, P) {
			t.Errorf("the password is an argument: %q", a)
		}
	}
}

// Scenario: С globals_role_passwords pg_dumpall запускается без --no-role-passwords.
func TestPgDumpallRunsWithRolePasswordsWhenAsked(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", say(psqlRow(180000, true, true, "backup"), nil, 0))
	want(t, r.backup(kg(o{"globals_role_passwords": true})), succeeded)
	c := r.proc.ran("pg_dumpall")[0]
	if !slices.Contains(c.Args, "--globals-only") || slices.Contains(c.Args, "--no-role-passwords") {
		t.Errorf("args = %q", c.Args)
	}
	if !slices.Contains(tagsOf(r.restic.backups()[1]), "postgresql.role_passwords=true") {
		t.Errorf("tags = %q", tagsOf(r.restic.backups()[1]))
	}
}

// Scenario: Пароли ролей без суперпользователя проваливают шаг до снимков.
func TestRolePasswordsWithoutASuperuserFailTheStepBeforeAnySnapshot(t *testing.T) {
	r := newRig(t)
	res := r.backup(kg(o{"globals_role_passwords": true}))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"backup"`, "globals_role_passwords", "superuser")
	r.lastPhase(t, preparing)
	r.noDump(t)
	if len(r.restic.run) != 0 {
		t.Errorf("restic ran %d times", len(r.restic.run))
	}
}

// Scenario: Отсутствие pg_dumpall рядом с pg_dump проваливает шаг с подсказкой о пакете.
func TestMissingPgDumpallNextToPgDumpFailsTheStep(t *testing.T) {
	r := newRig(t)
	delete(r.fs.nodes, "/usr/bin/pg_dumpall")
	res := r.backup(kg())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dumpall", "/usr/bin/pg_dumpall", "postgresql-client", "postgresql", "include_globals")
	oneLine(t, res.GetMessage())
	r.noDump(t)
}

// Scenario: pg_dumpall старше сервера проваливает шаг до дампа и называет обе версии.
func TestPgDumpallOlderThanTheServerFailsBeforeTheDump(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dumpall --version", say("pg_dumpall (PostgreSQL) 16.4\n", nil, 0))
	res := r.backup(kg())
	want(t, res, failed)
	mentions(t, res.GetMessage(), "pg_dumpall", "16.4", "18.0")
	r.noDump(t)
}

// Scenario: Сбой pg_dumpall проваливает шаг без единого снимка.
func TestPgDumpallFailureFailsTheStepWithoutAnySnapshot(t *testing.T) {
	cases := []struct {
		name   string
		run    behaviour
		reason string
	}{
		{"error", func(_ context.Context, c plugCmd) (int, error) {
			c.Stderr("pg_dumpall: error: permission denied")
			return 1, nil
		}, "permission denied"},
		{"no output", say("", nil, 0), "empty output"},
		{"too large", func(_ context.Context, c plugCmd) (int, error) {
			chunk := []byte(strings.Repeat("x", 1<<20))
			for i := 0; i < 64; i++ {
				if _, err := c.Stdout.Write(chunk); err != nil {
					return -1, err
				}
			}
			_, err := c.Stdout.Write([]byte("x"))
			return 0, err
		}, "64 MiB"},
		{"killed", func(context.Context, plugCmd) (int, error) { return -1, errKilled }, "signal: killed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.proc.on("pg_dumpall", c.run)
			res := r.backup(kg())
			want(t, res, failed)
			mentions(t, res.GetMessage(), "pg_dumpall", c.reason)
			oneLine(t, res.GetMessage())
			if len(r.restic.run) != 0 || len(r.proc.ran("pg_dump")) != 0 {
				t.Errorf("restic ran %d times, pg_dump %d", len(r.restic.run), len(r.proc.ran("pg_dump")))
			}
			noOutput(t, res)
		})
	}
}

// Scenario: Вывод pg_dumpall не попадает в лог шага и на диск.
func TestOutputOfPgDumpallIsNotLoggedAndNotOnDisk(t *testing.T) {
	const marker = "GLOBALS-MARKER-7f3a"
	r := newRig(t)
	r.proc.on("pg_dumpall", say("CREATE ROLE "+marker+";\n", nil, 0))
	res := r.backup(kg())
	want(t, res, succeeded)
	if strings.Contains(res.GetMessage(), marker) || strings.Contains(r.sink.text(), marker) {
		t.Errorf("the marker is in the message or the log: %q", r.sink.text())
	}
	if files := r.filesContaining(marker); len(files) != 0 {
		t.Errorf("files with the marker: %q", files)
	}
}

// Scenario: Сбой дампа базы отбрасывает глобальные объекты.
func TestDatabaseDumpFailureDiscardsTheGlobals(t *testing.T) {
	r := newRig(t)
	r.proc.on("pg_dump", func(_ context.Context, c plugCmd) (int, error) {
		_, _ = io.WriteString(c.Stdout, archive(1<<20))
		return 1, nil
	})
	want(t, r.backup(kg()), failed)
	if len(r.restic.stdin) != 0 || len(r.restic.backups()) != 1 {
		t.Errorf("snapshots %d, restic backups %d", len(r.restic.stdin), len(r.restic.backups()))
	}
}

// Scenario: Сбой сохранения глобальных объектов после снимка базы даёт FAILED со снимком базы.
func TestGlobalsSaveFailureAfterTheDatabaseSnapshotFailsWithTheSnapshot(t *testing.T) {
	r := newRig(t)
	r.restic.backup = func(ctx context.Context, n int, cmd resticCommand) int {
		if n == 0 {
			return r.restic.defaultBackup(ctx, n, cmd)
		}
		_ = slurp(cmd)
		cmd.Stderr([]byte("Fatal: unable to save: storage is full"))
		return 1
	}
	res := r.backup(kg())
	want(t, res, failed)
	if res.GetBackup().GetSnapshotId() != snapshotID(0) || res.GetBackup().GetRepositoryId() != repoID {
		t.Errorf("output = %v", res.GetBackup())
	}
	mentions(t, res.GetMessage(), "app.globals.sql", `repository "R"`, "storage is full")
	oneLine(t, res.GetMessage())
}

// Scenario: Отмена во время сохранения глобальных объектов оставляет только снимок базы.
func TestCancelWhileSavingTheGlobalsKeepsOnlyTheDatabaseSnapshot(t *testing.T) {
	r := newRig(t)
	saving := make(chan struct{})
	r.restic.backup = func(ctx context.Context, n int, cmd resticCommand) int {
		if n == 0 {
			return r.restic.defaultBackup(ctx, n, cmd)
		}
		close(saving)
		return r.restic.defaultBackup(ctx, n, cmd)
	}
	r.restic.hold = func(n int) bool { return n == 1 }
	r.exec.Submit(step(js(kg())))
	<-saving
	r.exec.Cancel("c1")
	res := r.result()
	want(t, res, cancelled)
	if len(r.restic.stdin) != 1 || res.GetBackup().GetSnapshotId() != snapshotID(0) {
		t.Errorf("snapshots %d, output %v", len(r.restic.stdin), res.GetBackup())
	}
}

// Scenario: Имя файла глобальных объектов строится из имени базы.
func TestNameOfTheGlobalsFileIsBuiltFromTheDatabaseName(t *testing.T) {
	cases := map[string]string{"app": "app.globals.sql", "my db": "my%20db.globals.sql", "a.b": "a%2Eb.globals.sql"}
	for db, file := range cases {
		r := newRig(t)
		want(t, r.backup(kg(o{"database": db})), succeeded)
		if got := flag(r.restic.backups()[1], "--stdin-filename"); got != file {
			t.Errorf("database %q: file %q, want %q", db, got, file)
		}
	}
}

// Scenario: Отмена во время pg_dumpall не оставляет снимков.
func TestCancelWhilePgDumpallRunsLeavesNoSnapshot(t *testing.T) {
	r := newRig(t)
	stopped := make(chan struct{})
	r.proc.on("pg_dumpall", func(ctx context.Context, _ plugCmd) (int, error) {
		<-ctx.Done()
		close(stopped)
		return -1, errTerminated
	})
	r.exec.Submit(step(js(kg())))
	waitFor(t, "pg_dumpall", func() bool { return len(r.proc.ran("pg_dumpall")) == 1 })
	r.exec.Cancel("c1")
	want(t, r.result(), cancelled)
	<-stopped
	if len(r.restic.run) != 0 || len(r.proc.ran("pg_dump")) != 0 {
		t.Errorf("restic ran %d times, pg_dump %d", len(r.restic.run), len(r.proc.ran("pg_dump")))
	}
}
