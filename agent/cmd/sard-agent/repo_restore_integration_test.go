// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Integration tests of repo restore with the pinned restic and a local
// repository: the scenarios tagged @integration of repo-restore.feature.

// restoreExecutor runs the real restic as the test process: a restore is
// asked to run as root (no user to change to), every other call as the
// service user, which a test cannot become.
type restoreExecutor struct {
	restic.ProcessExecutor
	t *testing.T
}

func (e restoreExecutor) Run(ctx context.Context, cmd restic.Command) (int, error) {
	asService := cmd.RunAs != nil && *cmd.RunAs == restic.RunAs{UID: serviceUID, GID: serviceUID}
	if isRestore := cmd.Args[0] == "restore"; isRestore == asService {
		e.t.Errorf("restic %v ran as %+v", cmd.Args, cmd.RunAs)
	}
	cmd.RunAs = nil
	return e.ProcessExecutor.Run(ctx, cmd)
}

// realRestoreHost is the host of repo restore with the real restic and the
// initialised repository base.
func realRestoreHost(t *testing.T) *setupHost {
	t.Helper()
	h := newSetupHost(t)
	h.cfg.Restic.Path = pinnedRestic(t)
	h.saveConfig()
	h.deps.exec = restoreExecutor{t: t}
	h.deps.pathEnv = os.Getenv("PATH")
	assertCode(t, first(h.sudo("repo", "init", "--config", "C", "base")), exitOK)
	return h
}

// backupBase makes a snapshot of dir in the repository base with the real restic.
func (h *setupHost) backupBase(dir string, tags ...string) {
	h.t.Helper()
	args := []string{"backup", "--no-cache", dir}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	cmd := exec.Command(pinnedRestic(h.t), args...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+h.path("base"), "RESTIC_PASSWORD_FILE="+h.path("secrets/base.pass"))
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("restic backup: %v\n%s", err, out)
	}
}

// fullID is the id of the only snapshot, read through the command.
func (h *setupHost) onlySnapshotID() string {
	h.t.Helper()
	_, stdout, _ := h.sudo("repo", "snapshots", "--config", "C", "--json", "base")
	id := hexID.FindString(stdout)
	if id == "" {
		h.t.Fatalf("no snapshot in %q", stdout)
	}
	return id
}

func sameFile(t *testing.T, want, got string) {
	t.Helper()
	wantData, err := os.ReadFile(want)
	ok(t, err)
	gotData, err := os.ReadFile(got)
	if err != nil || string(gotData) != string(wantData) {
		t.Fatalf("%s = %q (%v), want %q", got, gotData, err, wantData)
	}
	wantInfo, _ := os.Stat(want)
	gotInfo, _ := os.Stat(got)
	if gotInfo.Mode() != wantInfo.Mode() || !gotInfo.ModTime().Equal(wantInfo.ModTime()) {
		t.Errorf("%s: mode %v and time %v, want %v and %v", got, gotInfo.Mode(), gotInfo.ModTime(), wantInfo.Mode(), wantInfo.ModTime())
	}
}

// Файлы снимка ложатся под исходным путём внутри цели байт в байт
func TestIntegrationTheFilesOfASnapshotLieUnderTheirOriginalPathsInTheTarget(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o640)
	h.write(src+"/sub/b.txt", "BBB", 0o600)
	ok(t, os.Chtimes(src+"/a.txt", time.Unix(1_700_000_000, 0), time.Unix(1_700_000_000, 0)))
	h.backupBase(src)
	code, stdout, stderr := h.restore(h.onlySnapshotID())
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
	sameFile(t, src+"/sub/b.txt", filepath.Join(h.target(), src, "sub/b.txt"))
	if !hexID.MatchString(stdout) {
		t.Errorf("stdout %q", stdout)
	}
}

// Несколько path восстанавливают только эти пути
func TestIntegrationSeveralPathsRestoreOnlyThosePaths(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "A", 0o644)
	h.write(src+"/b.txt", "B", 0o644)
	h.write(src+"/sub/c.txt", "C", 0o644)
	h.backupBase(src)
	code, _, stderr := h.restore(h.onlySnapshotID(), "--path", src+"/a.txt", "--path", src+"/sub")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
	sameFile(t, src+"/sub/c.txt", filepath.Join(h.target(), src, "sub/c.txt"))
	h.assertAbsent(filepath.Join(h.target(), src, "b.txt"))
}

// A path that is not in the snapshot is found by the real restic before anything is written.
func TestIntegrationAPathOfNoFileIsRefusedBeforeTheTargetIsMade(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "A", 0o644)
	h.backupBase(src)
	code, _, stderr := h.restore(h.onlySnapshotID(), "--path", src+"/nothing")
	assertRefusal(t, code, stderr, exitUsage, "PATH_NOT_IN_SNAPSHOT")
	h.assertAbsent(h.target())
}

// На новом хосте репозиторий, подключённый с паролем из копии, восстанавливается
func TestIntegrationARepositoryAttachedWithThePasswordOfACopyIsRestoredWithoutRegistration(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o644)
	h.backupBase(src, "sard.run=r1", "sard.source=s1")
	copyOfPassword := h.path("outside/copy.pass")
	h.write(copyOfPassword, passMarker+"\n", 0o600)
	// the new host: no repositories, no tls files, a server that is not there
	h.cfg.Repositories = nil
	h.cfg.Server.Address = "127.0.0.1:1"
	h.saveConfig()
	code, _, stderr := h.sudo("repo", "add", "main", h.path("base"), "--password-from-file", copyOfPassword, "--no-restart", "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	code, _, stderr = h.sudo("repo", "restore", "main", "latest", "--source", "s1", "--target", h.target(), "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	sameFile(t, src+"/a.txt", filepath.Join(h.target(), src, "a.txt"))
}

// backupStdinBase makes a snapshot of the stream with the real restic: one
// file called name at the root of the snapshot, as the plugins that dump a
// database make them (/app.dump).
func (h *setupHost) backupStdinBase(name, content string, tags ...string) {
	h.t.Helper()
	args := []string{"backup", "--no-cache", "--stdin", "--stdin-filename", name}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	cmd := exec.Command(pinnedRestic(h.t), args...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+h.path("base"), "RESTIC_PASSWORD_FILE="+h.path("secrets/base.pass"))
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("restic backup --stdin: %v\n%s", err, out)
	}
}

// Снимок с файлом в корне восстанавливается через временный каталог
func TestIntegrationASnapshotWithAFileAtTheRootIsRestoredThroughTheTemporaryDirectory(t *testing.T) {
	h := realRestoreHost(t)
	h.backupStdinBase("app.dump", "DUMP-BYTES")
	code, _, stderr := h.restore(h.onlySnapshotID())
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	if got := h.fileContent(h.target() + "/app.dump"); got != "DUMP-BYTES" {
		t.Errorf("app.dump = %q", got)
	}
	h.assertNoStaging()
}

// ownerForTest is the owner the files of the tree get: another user than the
// one who runs the test, when the test may change owners.
func ownerForTest() int {
	if os.Geteuid() == 0 {
		return 1001
	}
	return os.Getuid()
}

// treeWithMetadata makes D/src: files of another owner with fixed times, a
// hard link, a FIFO and a file with the setuid bit.
func treeWithMetadata(h *setupHost, src string, owner int, mtime time.Time) {
	h.write(src+"/a.txt", "AAA", 0o640)
	h.write(src+"/sub/deep/b.txt", "BBB", 0o600)
	h.write(src+"/setuid", "S", 0o750)
	ok(h.t, os.Link(src+"/a.txt", src+"/sub/hard"))
	ok(h.t, syscall.Mkfifo(src+"/fifo", 0o600))
	for _, p := range []string{"a.txt", "sub/deep/b.txt", "setuid", "sub/deep", "sub"} {
		ok(h.t, os.Lchown(src+"/"+p, owner, owner))
		ok(h.t, os.Chtimes(src+"/"+p, mtime, mtime))
	}
	ok(h.t, os.Chmod(src+"/setuid", os.ModeSetuid|0o750)) // after the owner: a change of owner clears the bit
}

// assertSpecialFiles: the hard links share an inode, the FIFO is a FIFO, the
// setuid bit is there.
func assertSpecialFiles(t *testing.T, out string) {
	t.Helper()
	if a, hard := statOf(t, out+"/a.txt"), statOf(t, out+"/sub/hard"); a.Ino != hard.Ino {
		t.Errorf("the hard links do not share an inode: %d and %d", a.Ino, hard.Ino)
	}
	if info, err := os.Lstat(out + "/fifo"); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("fifo: %v, %v", info, err)
	}
	if info, err := os.Lstat(out + "/setuid"); err != nil || info.Mode()&os.ModeSetuid == 0 || info.Mode().Perm() != 0o750 {
		t.Errorf("setuid: %v, %v", info, err)
	}
}

// assertOwnersAndTimes: the files have the owner and the time of the snapshot.
func assertOwnersAndTimes(t *testing.T, out string, owner int, mtime time.Time) {
	t.Helper()
	for _, p := range []string{"a.txt", "sub/deep/b.txt", "setuid", "sub/deep"} {
		st := statOf(t, out+"/"+p)
		if int(st.Uid) != owner || int(st.Gid) != owner {
			t.Errorf("%s: owner %d:%d, want %d", p, st.Uid, st.Gid, owner)
		}
		if info, _ := os.Lstat(out + "/" + p); !info.ModTime().Equal(mtime) {
			t.Errorf("%s: mtime %v, want %v", p, info.ModTime(), mtime)
		}
	}
}

// Перенос сохраняет жёсткие ссылки, FIFO, владельцев, права и время без копирования
func TestIntegrationTheMoveKeepsHardLinksFIFOsOwnersModesAndTimes(t *testing.T) {
	h := realRestoreHost(t)
	src, owner, mtime := h.path("src"), ownerForTest(), time.Unix(1_700_000_000, 0)
	treeWithMetadata(h, src, owner, mtime)
	h.backupBase(src)
	code, _, stderr := h.restore(h.onlySnapshotID())
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	out := filepath.Join(h.target(), src)
	assertSpecialFiles(t, out)
	assertOwnersAndTimes(t, out, owner, mtime)
	h.assertNoStaging()
}

func statOf(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	info, err := os.Lstat(path)
	ok(t, err)
	return info.Sys().(*syscall.Stat_t)
}

// Подмена имени в цели во время восстановления не уводит запись за её пределы
// (цикл подмены от имени пользователя службы — @vm; здесь тот же приём от
// имени теста).
func TestIntegrationANameSwappedForALinkInTheTargetWhileRestoringLeadsNowhere(t *testing.T) {
	h := realRestoreHost(t)
	src := h.path("src")
	h.write(src+"/a.txt", "AAA", 0o640)
	h.backupBase(src)
	outside := h.path("outside")
	ok(t, os.MkdirAll(outside, 0o700))
	ok(t, os.MkdirAll(h.target(), 0o700))
	first := strings.Split(strings.TrimPrefix(filepath.Clean(src), "/"), "/")[0]
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.RemoveAll(filepath.Join(h.target(), first))
			_ = os.Symlink(outside, filepath.Join(h.target(), first))
		}
	}()
	code, _, stderr := h.restore(h.onlySnapshotID())
	close(stop)
	<-stopped
	if code != exitOK && code != exitUsage && code != exitWrite {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Errorf("something was written outside the target: %v, %v", entries, err)
	}
}
