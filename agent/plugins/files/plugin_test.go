// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// --- the config is checked before anything happens

// Scenario: Конфиг, нарушающий схему, отклоняется с указанием поля.
func TestAConfigThatBreaksTheSchemaIsRejectedNamingTheField(t *testing.T) {
	for _, tc := range []struct{ cfg, field string }{
		{`{}`, "(root)"},
		{`{"paths": []}`, "/paths"},
		{`{"paths": "/etc"}`, "/paths"},
		{`{"paths": [42]}`, "/paths/0"},
		{`{"paths": ["etc"]}`, "/paths/0"},
		{`{"paths": ["./etc"]}`, "/paths/0"},
		{`{"paths": ["~/data"]}`, "/paths/0"},
		{`{"paths": [""]}`, "/paths/0"},
		{`{"paths": ["/etc", "/etc"]}`, "/paths"},
		{`{"paths": ["/etc"], "exclude": "*.log"}`, "/exclude"},
		{`{"paths": ["/etc"], "exclude": [""]}`, "/exclude/0"},
		{`{"paths": ["/etc"], "exclude": [7]}`, "/exclude/0"},
		{`{"paths": ["/etc"], "one_file_system": "yes"}`, "/one_file_system"},
		{`{"paths": ["/etc"], "extra": 1}`, "(root)"},
		{`not json`, "(root)"},
		// Scenario: Путь с символом NUL отклоняется.
		{`{"paths": ["/etc\u0000x"]}`, "/paths/0"},
		{`{"paths": ["/etc"], "exclude": ["a\u0000b"]}`, "/exclude/0"},
	} {
		r := newRig(t)
		res := r.run(step(tc.cfg))
		want(t, res, rejected)
		mentions(t, res.GetMessage(), " "+tc.field+": ")
		noOutput(t, res)
		if r.restic.ran() != 0 {
			t.Errorf("%s: restic ran", tc.cfg)
		}
	}
}

// Scenario: Число и длина путей и шаблонов на границе пределов.
func TestTheLimitsOfPathsAndPatternsAreTheirBoundary(t *testing.T) {
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("/d%d", i)
		}
		return out
	}
	long := func(n int) string { return "/" + strings.Repeat("a", n-1) }
	patterns := func(n int, size int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%q", strings.Repeat("x", size))
		}
		return `{"paths": ["/d"], "exclude": [` + strings.Join(out, ",") + `]}`
	}
	for name, tc := range map[string]struct {
		cfg    string
		status agentv1.StepStatus
	}{
		"64 existing directories":                   {paths(many(64)...), succeeded},
		"65 paths":                                  {paths(many(65)...), rejected},
		"a missing path of 4096":                    {paths(long(4096)), failed},
		"a path of 4097 bytes":                      {paths(long(4097)), rejected},
		"256 patterns":                              {patterns(256, 1), succeeded},
		"257 patterns":                              {patterns(257, 1), rejected},
		"a pattern of 1024 bytes":                   {patterns(1, 1024), succeeded},
		"a pattern of 1025 bytes":                   {patterns(1, 1025), rejected},
		"a pattern of 1025 bytes in 513 characters": {`{"paths": ["/d"], "exclude": ["` + strings.Repeat("é", 512) + `a"]}`, rejected},
		"a path of 2049 two-byte ":                  {paths("/" + strings.Repeat("é", 2048)), rejected},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.fs.dir("/d")
			r.fs.dir(many(64)...)
			res := r.run(step(tc.cfg))
			want(t, res, tc.status)
		})
	}
}

// Scenario: Повтор после нормализации и вложенные пути отклоняются.
func TestRepeatsAfterNormalisationAndNestedPathsAreRejected(t *testing.T) {
	for _, p := range [][2]string{
		{"/data", "/data/"},
		{"/data", "/data/sub/.."},
		{"/data", "/data/sub"},
		{"/data/sub", "/data"},
		{"/", "/data"},
	} {
		r := newRig(t)
		r.fs.dir("/data", "/data/sub")
		res := r.run(step(paths(p[0], p[1])))
		want(t, res, rejected)
		mentions(t, res.GetMessage(), fmt.Sprintf("%q", p[0]), fmt.Sprintf("%q", p[1]))
		noOutput(t, res)
		if r.restic.ran() != 0 || len(r.fs.lstats) != 0 {
			t.Errorf("%v: restic ran %d, host looked at %v", p, r.restic.ran(), r.fs.lstats)
		}
	}
}

// Siblings that merely share a prefix are not nested.
func TestPathsThatShareOnlyAPrefixAreNotNested(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/data", "/data2", "/data.d")
	want(t, r.run(step(paths("/data", "/data2", "/data.d"))), succeeded)
}

// Scenario: Метка шага с запятой отклоняется до запуска restic.
func TestAStepTagWithACommaIsRejectedBeforeResticRuns(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	s := step(paths("/D"))
	s.Tags = map[string]string{"source": "a,b"}
	want(t, r.run(s), rejected)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Шаг с неизвестным репозиторием отклоняется.
func TestAStepForAnUnknownRepositoryIsRejected(t *testing.T) {
	r := newRig(t)
	s := step(paths("/D"))
	s.RepositoryName = "nope"
	res := r.run(s)
	want(t, res, rejected)
	mentions(t, res.GetMessage(), "nope")
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Несуществующий путь в конфиге не нарушает схему.
func TestAPathThatDoesNotExistIsNotAConfigError(t *testing.T) {
	r := newRig(t)
	want(t, r.run(step(paths("/nonexistent"))), failed)
}

// --- the listed paths are checked before restic runs

const secretMarker = "QA-MARKER-5c1e"

// Scenario: Непрошедший путь называется с причиной, бэкап не начинается.
func TestAPathThatFailsTheCheckIsNamedWithItsReasonAndNothingIsBackedUp(t *testing.T) {
	denied := func(op, p string) error { return pathError(op, p, 13) } // EACCES
	for _, tc := range []struct {
		path, reason string
		setup        func(f *fakeFS)
	}{
		{"/missing", "no such file or directory", func(*fakeFS) {}},
		{"/file.txt/x", "no such file or directory", func(f *fakeFS) {
			f.file("/file.txt")
			f.set("/file.txt/x", node{lstatErr: pathError("lstat", "/file.txt/x", 20)}) // ENOTDIR
		}},
		{"/locked", "permission denied", func(f *fakeFS) {
			f.set("/locked", node{mode: 0o000 | fs.ModeDir, openErr: denied("open", "/locked")})
		}},
		{"/secret.txt", "permission denied", func(f *fakeFS) {
			f.set("/secret.txt", node{mode: 0o000, openErr: denied("open", "/secret.txt")})
		}},
		{"/noexec/dir", "permission denied", func(f *fakeFS) {
			f.dir("/noexec")
			f.set("/noexec/dir", node{lstatErr: denied("lstat", "/noexec/dir")})
		}},
		{"/unlistable", "permission denied", func(f *fakeFS) {
			f.set("/unlistable", node{mode: fs.ModeDir, listErr: denied("readdirent", "/unlistable")})
		}},
		{"/unowned", "permission denied", func(f *fakeFS) {
			f.set("/unowned", node{mode: 0o644, openErr: pathError("open", "/unowned", 1)}) // EPERM
		}},
		{"/broken", "input/output error", func(f *fakeFS) {
			f.set("/broken", node{mode: fs.ModeDir, openErr: pathError("open", "/broken", 5)}) // EIO
		}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := newRig(t)
			r.fs.dir("/D")
			tc.setup(r.fs)
			res := r.run(step(paths("/D", tc.path)))
			want(t, res, failed)
			wantMsg := fmt.Sprintf("prepare: paths that cannot be backed up (1): %q: %s", tc.path, tc.reason)
			if res.GetMessage() != wantMsg {
				t.Errorf("message = %q, want %q", res.GetMessage(), wantMsg)
			}
			omits(t, res.GetMessage(), `"/D"`)
			oneLine(t, res.GetMessage())
			noOutput(t, res)
			if r.restic.ran() != 0 {
				t.Error("restic ran")
			}
			if got := r.sink.phases(); got[len(got)-1] != agentv1.StepPhase_STEP_PHASE_PREPARING {
				t.Errorf("phases = %v", got)
			}
		})
	}
}

// Scenario: Все непрошедшие пути перечисляются в одном сообщении.
func TestEveryFailedPathIsListedInOneMessage(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.fs.set("/c", node{mode: fs.ModeDir, openErr: pathError("open", "/c", 13)})
	res := r.run(step(paths("/a", "/b", "/c", "/D")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"/a": no such file or directory`, `"/b": no such file or directory`, `"/c": permission denied`, "(3)")
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Больше десяти непрошедших путей - названы первые десять и общее число.
func TestMoreThanTenFailedPathsNameTheFirstTenAndTheirNumber(t *testing.T) {
	r := newRig(t)
	var list []string
	for i := 1; i <= 12; i++ {
		list = append(list, fmt.Sprintf("/p%d", i))
	}
	res := r.run(step(paths(list...)))
	want(t, res, failed)
	mentions(t, res.GetMessage(), "(12), first 10:", `"/p1"`, `"/p10"`)
	omits(t, res.GetMessage(), `"/p11"`, `"/p12"`)
	if first, tenth := strings.Index(res.GetMessage(), `"/p1"`), strings.Index(res.GetMessage(), `"/p10"`); first > tenth {
		t.Errorf("paths are not in the order of the config: %q", res.GetMessage())
	}
}

// Exactly ten failed paths are all named; "first ten" is only for more.
func TestTenFailedPathsAreAllNamedWithoutFirstTen(t *testing.T) {
	r := newRig(t)
	var list []string
	for i := 1; i <= 10; i++ {
		list = append(list, fmt.Sprintf("/p%d", i))
	}
	res := r.run(step(paths(list...)))
	want(t, res, failed)
	mentions(t, res.GetMessage(), "(10):", `"/p10"`)
	omits(t, res.GetMessage(), "first")
}

// Scenario: Проверка путей не читает содержимое файлов.
func TestTheCheckDoesNotRevealOrReadContent(t *testing.T) {
	r := newRig(t)
	r.fs.set("/secret.txt", node{mode: 0o000, openErr: pathError("open", "/secret.txt", 13)})
	res := r.run(step(paths("/secret.txt")))
	want(t, res, failed)
	omits(t, res.GetMessage(), secretMarker)
	for _, l := range r.sink.logs {
		omits(t, l, secretMarker)
	}
}

// Scenario: Указанный путь-симлинк проваливает шаг до бэкапа.
func TestAListedSymbolicLinkFailsTheStepBeforeTheBackup(t *testing.T) {
	r := newRig(t)
	r.fs.set("/data", node{mode: fs.ModeSymlink, target: "/mnt/data"}) // fs.ModeSymlink
	res := r.run(step(paths("/data")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"/data"`, "symbolic link", `"/mnt/data"`)
	noOutput(t, res)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

func TestAListedSymbolicLinkWhoseTargetCannotBeReadIsStillNamed(t *testing.T) {
	r := newRig(t)
	r.fs.set("/data", node{mode: fs.ModeSymlink})
	res := r.run(step(paths("/data")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"/data": is a symbolic link; back up the path it points to`)
	omits(t, res.GetMessage(), `to ""`)
}

// A directory that opens is fine, so is a file; an empty directory too.
func TestReadableFilesAndDirectoriesPassTheCheck(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D", "/E").file("/F")
	r.fs.set("/sock", node{mode: fs.ModeSocket}) // not opened
	want(t, r.run(step(paths("/D", "/E", "/F", "/sock"))), succeeded)
	if len(r.fs.reads) != 3 || len(r.fs.listing) != 2 {
		t.Errorf("opened %v, listed %v", r.fs.reads, r.fs.listing)
	}
	if !slices.Equal(r.fs.listN, []int{1, 1}) {
		t.Errorf("a directory is asked for %v names, want one each", r.fs.listN)
	}
	if r.fs.opened != r.fs.closed {
		t.Errorf("opened %d, closed %d", r.fs.opened, r.fs.closed)
	}
}

// Scenario: Отмена во время проверки путей не запускает restic.
func TestCancelDuringThePathCheckStopsBeforeRestic(t *testing.T) {
	r := newRig(t)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	r.fs.set("/slow", node{mode: fs.ModeDir, gate: gate})
	r.exec.Submit(step(paths("/slow")))
	waitFor(t, "the check to start", func() bool { r.fs.mu.Lock(); defer r.fs.mu.Unlock(); return len(r.fs.lstats) > 0 })
	r.exec.Cancel("c1")
	want(t, r.result(), cancelled)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// --- actions

// Scenario: Восстановление плагином files завершается FAILED без restic и без изменений на диске.
func TestRestoreOfFilesFailsWithoutResticAndWithoutChangesOnDisk(t *testing.T) {
	r := newRig(t)
	before := tree(t, r.stateDir)
	s := step(paths("/D"))
	s.Action, s.SnapshotId = agentv1.Action_ACTION_RESTORE, "abc"
	res := r.run(s)
	want(t, res, failed)
	mentions(t, res.GetMessage(), "restore", "files", "not implemented yet")
	noOutput(t, res)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
	if _, err := os.Stat(filepath.Join(r.restoreDir, "c1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore directory: %v", err)
	}
	// Only the saved result of the step is new.
	for _, p := range tree(t, r.stateDir) {
		if !slices.Contains(before, p) && !strings.HasPrefix(p, "results") {
			t.Errorf("new entry %q in the state dir", p)
		}
	}
}

// Scenario: Восстановление плагином files с пустым snapshot_id отклоняется.
func TestRestoreOfFilesWithoutASnapshotIsRejected(t *testing.T) {
	r := newRig(t)
	s := step(paths("/D"))
	s.Action = agentv1.Action_ACTION_RESTORE
	want(t, r.run(s), rejected)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Восстановление плагином files в неизвестный репозиторий отклоняется.
func TestRestoreOfFilesIntoAnUnknownRepositoryIsRejected(t *testing.T) {
	r := newRig(t)
	s := step(paths("/D"))
	s.Action, s.SnapshotId, s.RepositoryName = agentv1.Action_ACTION_RESTORE, "abc", "nope"
	want(t, r.run(s), rejected)
	if r.restic.ran() != 0 {
		t.Error("restic ran")
	}
}

// Scenario: Проверка и запуск скрипта плагином files отклоняются как неподдерживаемые.
func TestVerifyAndRunAreRejectedAsUnsupported(t *testing.T) {
	for _, action := range []agentv1.Action{agentv1.Action_ACTION_VERIFY, agentv1.Action_ACTION_RUN} {
		r := newRig(t)
		s := step(paths("/D"))
		s.Action, s.SnapshotId = action, "abc"
		res := r.run(s)
		want(t, res, rejected)
		mentions(t, res.GetMessage(), "files", action.String())
		noOutput(t, res)
		if r.restic.ran() != 0 {
			t.Error("restic ran")
		}
	}
}

// --- the backup

// The snapshot, the sizes and the repository are reported; restic gets the
// tags of the step in order.
func TestABackupReportsTheSnapshotItsSizesAndTheRepository(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	res := r.run(step(paths("/D")))
	want(t, res, succeeded)
	if res.GetMessage() != "" {
		t.Errorf("message = %q", res.GetMessage())
	}
	out := res.GetBackup()
	if out.GetSnapshotId() != snapshotID || out.GetRepositoryId() != repoID || out.GetTotalBytes() != 1024 || out.GetAddedBytes() != 1500 {
		t.Errorf("output = %v", out)
	}
}

func TestResticGetsTheTagsOfTheStepInOrderAndThePath(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	want(t, r.run(step(paths("/D"))), succeeded)
	args := r.restic.backupArgs(t)
	if i := slices.Index(args, "--tag"); i < 0 || args[i+1] != "run=r1" || args[i+3] != "source=s1" {
		t.Errorf("args = %q", args)
	}
	if tail := args[len(args)-2:]; !slices.Equal(tail, []string{"--", "/D"}) {
		t.Errorf("args = %q", args)
	}
}

func TestListedPathsGoToResticNormalised(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/a", "/b")
	want(t, r.run(step(paths("/a/", "/x/../b"))), succeeded)
	if args := r.restic.backupArgs(t); !slices.Equal(args[len(args)-3:], []string{"--", "/a", "/b"}) {
		t.Errorf("args = %q", args)
	}
}

// Scenario: Нечитаемый файл под exclude не делает шаг неуспешным.
func TestExcludePatternsGoToResticAsTheyAre(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	want(t, r.run(step(`{"paths": ["/D"], "exclude": ["secret", "*.log"]}`)), succeeded)
	args := strings.Join(r.restic.backupArgs(t), " ")
	if !strings.Contains(args, "--exclude secret --exclude *.log") {
		t.Errorf("args = %q", args)
	}
}

// Scenario: Шаблон исключения, который restic не принимает, проваливает шаг без снимка.
func TestAnExcludePatternResticRefusesFailsTheStepWithItsReason(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = script(nil, []string{exitLine(1, "Fatal: --exclude: invalid pattern(s) provided:\n[")}, 1)
	res := r.run(step(`{"paths": ["/D"], "exclude": ["["]}`))
	want(t, res, failed)
	mentions(t, res.GetMessage(), "invalid pattern", "[")
	oneLine(t, res.GetMessage())
	noOutput(t, res)
}

// Scenario: По умолчанию restic переходит на другие файловые системы /
// Настройка one_file_system запрещает restic переходить на другие файловые системы.
func TestOneFileSystemIsOffByDefaultAndPassedWhenSet(t *testing.T) {
	for cfg, wantFlag := range map[string]bool{
		`{"paths": ["/D"]}`:                           false,
		`{"paths": ["/D"], "one_file_system": false}`: false,
		`{"paths": ["/D"], "one_file_system": true}`:  true,
	} {
		r := newRig(t)
		r.fs.dir("/D")
		want(t, r.run(step(cfg)), succeeded)
		if got := slices.Contains(r.restic.backupArgs(t), "--one-file-system"); got != wantFlag {
			t.Errorf("%s: flag = %v", cfg, got)
		}
	}
}

// --- files restic could not read

func unreadableRun(items ...string) func(context.Context, restic.Command) int {
	var stderr []string
	for _, i := range items {
		stderr = append(stderr, errorLine(i, "archival"))
	}
	stderr = append(stderr, exitLine(3, "Warning: at least one source file could not be read"))
	return script([]string{summaryLine(snapshotID)}, stderr, 3)
}

// Scenario: Нечитаемые файлы внутри дерева дают FAILED со снимком в результате.
func TestUnreadableFilesInTheTreeFailTheStepButKeepTheSnapshot(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = script([]string{summaryLine(snapshotID)}, []string{
		errorLine("/D/locked", "scan"), errorLine("/D/locked", "archival"), errorLine("/D/sub/x.txt", "archival"),
		exitLine(3, "Warning: at least one source file could not be read"),
	}, 3)
	res := r.run(step(paths("/D")))
	want(t, res, failed)
	if out := res.GetBackup(); out.GetSnapshotId() != snapshotID || out.GetRepositoryId() != repoID {
		t.Errorf("output = %v", res.GetOutput())
	}
	mentions(t, res.GetMessage(), "(2)", `"/D/locked"`, `"/D/sub/x.txt"`)
	omits(t, res.GetMessage(), secretMarker)
	oneLine(t, res.GetMessage())
}

// Scenario: Больше десяти нечитаемых файлов.
func TestMoreThanTenUnreadableFilesNameTheFirstTenAndTheirNumber(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	var items []string
	for i := 1; i <= 11; i++ {
		items = append(items, fmt.Sprintf("/D/f%d", i))
	}
	r.restic.backup = unreadableRun(items...)
	res := r.run(step(paths("/D")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), "(11)", `"/D/f1"`, `"/D/f10"`)
	omits(t, res.GetMessage(), `"/D/f11"`)
	if res.GetBackup().GetSnapshotId() == "" {
		t.Error("no snapshot in the result")
	}
}

// Scenario: Файл, исчезнувший во время бэкапа, считается нечитаемым.
func TestAFileThatVanishedDuringTheBackupCountsAsUnreadable(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = script([]string{summaryLine(snapshotID)}, []string{
		`{"message_type":"error","error":{"message":"lstat /D/tmp.1: no such file or directory"},"during":"archival","item":"/D/tmp.1"}`,
		exitLine(3, "Warning: at least one source file could not be read"),
	}, 3)
	res := r.run(step(paths("/D")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), "(1)", `"/D/tmp.1"`)
	if res.GetBackup().GetSnapshotId() == "" {
		t.Error("no snapshot in the result")
	}
}

// Scenario: Имя нечитаемого файла с переводом строки не ломает сообщение.
func TestAnUnreadableFileNameWithALineBreakDoesNotBreakTheMessage(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = unreadableRun("/D/a\nb")
	res := r.run(step(paths("/D")))
	oneLine(t, res.GetMessage())
	mentions(t, res.GetMessage(), `"/D/a\nb"`)
}

// Scenario: Путь, исчезнувший после проверки, не даёт успешного шага.
func TestAPathThatVanishedAfterTheCheckDoesNotGiveASuccessfulStep(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/gone", "/D")
	r.restic.backup = script([]string{summaryLine(snapshotID)}, []string{
		"/gone does not exist, skipping",
		exitLine(3, "Warning: at least one source file could not be read"),
	}, 3)
	res := r.run(step(paths("/gone", "/D")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"/gone"`)
}

func TestTheOnlyPathVanishedAfterTheCheckIsNamed(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/gone")
	r.restic.backup = script(nil, []string{
		"/gone does not exist, skipping",
		exitLine(1, "Fatal: all source directories/files do not exist"),
	}, 1)
	res := r.run(step(paths("/gone")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"/gone"`)
	noOutput(t, res)
}

// --- progress

// Scenario: Фазы шага идут от проверки путей к загрузке.
func TestThePhasesGoFromThePathCheckToTheUpload(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	want(t, r.run(step(paths("/D"))), succeeded)
	wantPhases := []agentv1.StepPhase{
		agentv1.StepPhase_STEP_PHASE_ACCEPTED, agentv1.StepPhase_STEP_PHASE_PREPARING,
		agentv1.StepPhase_STEP_PHASE_DUMPING, agentv1.StepPhase_STEP_PHASE_UPLOADING,
	}
	if got := r.sink.phases(); !slices.Equal(got, wantPhases) {
		t.Errorf("phases = %v", got)
	}
	for _, phase := range wantPhases[1:3] {
		for _, p := range r.sink.reports(phase) {
			if p.GetBytesProcessed() != 0 || p.GetBytesTotal() != 0 {
				t.Errorf("%v: %v", phase, p)
			}
		}
	}
}

// Scenario: Счётчик байт и файлов в загрузке не убывает.
func TestByteAndFileCountersOfTheUploadReachTheServerInOrder(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = script([]string{
		statusLine(100, 1), statusLine(400, 5), statusLine(1024, 9), summaryLine(snapshotID),
	}, nil, 0)
	want(t, r.run(step(paths("/D"))), succeeded)
	var bytes, filesDone []uint64
	for _, p := range r.sink.reports(agentv1.StepPhase_STEP_PHASE_UPLOADING) {
		if p.GetBytesProcessed() == 0 && p.GetFilesProcessed() == 0 {
			continue // the entry into the phase
		}
		bytes = append(bytes, p.GetBytesProcessed())
		filesDone = append(filesDone, p.GetFilesProcessed())
		if p.GetBytesTotal() != 1024 || p.GetFilesTotal() != 9 {
			t.Errorf("totals: %v", p)
		}
	}
	if !slices.Equal(bytes, []uint64{100, 400, 1024}) || !slices.Equal(filesDone, []uint64{1, 5, 9}) {
		t.Errorf("bytes %v, files %v", bytes, filesDone)
	}
}

// --- the repository

// Scenario: Заблокированный репозиторий ждут ограниченное время средствами restic.
func TestALockedRepositoryIsWaitedForByRestic(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	want(t, r.run(step(paths("/D"))), succeeded)
	args := r.restic.backupArgs(t)
	if i := slices.Index(args, "--retry-lock"); i < 0 || args[i+1] != "5m" {
		t.Errorf("args = %q", args)
	}
}

// Scenario: Репозиторий, не освободившийся за время ожидания, проваливает шаг.
func TestARepositoryThatStaysLockedFailsTheStepNamingIt(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = script(nil, []string{exitLine(11, "Fatal: unable to create lock in backend: repository is already locked")}, 11)
	res := r.run(step(paths("/D")))
	want(t, res, failed)
	mentions(t, res.GetMessage(), `"R"`, "locked")
	noOutput(t, res)
}

// Scenario: Недоступный или неверный репозиторий проваливает шаг без снимка.
func TestAnUnusableRepositoryFailsTheStepNamingItAndTheReason(t *testing.T) {
	for name, tc := range map[string]struct {
		run    func(context.Context, restic.Command) int
		start  error
		reason string
	}{
		"not initialised": {script(nil, []string{exitLine(10, "Fatal: repository does not exist")}, 10), nil, "repository does not exist"},
		"wrong password":  {script(nil, []string{exitLine(12, "Fatal: wrong password or no key found")}, 12), nil, "wrong password or no key found"},
		"unreachable":     {script(nil, []string{exitLine(1, "Fatal: unable to open repository at /srv: connection refused")}, 1), nil, "Fatal: unable to open repository"},
		"out of space":    {script(nil, []string{exitLine(1, "Fatal: no space left on device")}, 1), nil, "no space left on device"},
		"no restic":       {nil, errors.New("fork/exec /opt/sard/restic: no such file or directory"), "fork/exec /opt/sard/restic"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.fs.dir("/D")
			r.restic.backup, r.restic.startErr = tc.run, tc.start
			res := r.run(step(paths("/D")))
			want(t, res, failed)
			mentions(t, res.GetMessage(), `"R"`, tc.reason)
			omits(t, res.GetMessage(), "R.pass")
			noOutput(t, res)
		})
	}
}

// Scenario: Таймаут шага прерывает ожидание блокировки.
func TestTheStepTimeoutEndsTheWaitForALock(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	stopped := make(chan struct{})
	r.restic.backup = func(ctx context.Context, _ restic.Command) int {
		<-ctx.Done() // restic waits for the lock
		close(stopped)
		return 130
	}
	want(t, r.run(withTimeout(step(paths("/D")), 50*time.Millisecond)), timedOut)
	select {
	case <-stopped:
	default:
		t.Error("restic was not stopped")
	}
}

// Scenario: Отмена после того, как restic сохранил снимок, не теряет снимок.
func TestACancelAfterResticSavedTheSnapshotKeepsTheSnapshot(t *testing.T) {
	r := newRig(t)
	r.fs.dir("/D")
	r.restic.backup = func(_ context.Context, cmd restic.Command) int {
		cmd.Stdout([]byte(summaryLine(snapshotID)))
		r.exec.Cancel("c1") // arrives after restic printed the summary and before it returned
		return 0
	}
	res := r.run(step(paths("/D")))
	want(t, res, succeeded)
	if res.GetBackup().GetSnapshotId() != snapshotID {
		t.Errorf("output = %v", res.GetOutput())
	}
}
