// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore_test

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

const (
	snapshotID = "4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e4f1c2a9e"
	destRel    = "s1/20261010T030000Z"
)

// --- a file system that fails on demand

type faults struct {
	mu           sync.Mutex
	mkdirErr     map[string]error // by the path of the new directory
	removeDirErr map[string]error // by the path of the directory removed
	moveErr      error
	renameErr    error            // of a rename inside a directory
	syncErr      map[string]error // by the path of the directory synced
	moveHook     func()
	device       map[string]uint64 // by the path of the directory
	removeErr    map[string]error  // by the path of the file removed
	removed      []string          // paths of every Remove and RemoveDir, in order

	rootErr    error            // of OpenRootDir
	openErr    map[string]error // by the path of the directory opened
	statErr    map[string]error // by the path of the directory
	namesErr   map[string]error // by the path of the directory listed
	createErr  map[string]error // by the path of the file created
	createSkip map[string]int   // creates of that path that succeed before createErr applies
	fileErr    map[string]error // Sync of a file, by its path
	writeErr   map[string]error // Write of a file, by its path
	readErr    map[string]error // Read of a file opened for reading, by its path
	openFile   map[string]error // OpenFile, by the path of the file
	ops        []string         // "remove <path>" and "sync <path>", in order
	opened     int              // descriptors opened (directories and files)
	closed     int              // descriptors closed
}

// noLeaks fails the test if a directory or a file was opened and not closed
// (or closed twice).
func (f *faults) noLeaks(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opened != f.closed {
		t.Errorf("%d descriptors opened, %d closed", f.opened, f.closed)
	}
}

// skipCreate reports whether the create of the path fails now: the creates
// that createSkip lets succeed come first.
func (f *faults) skipCreate(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createSkip[path] > 0 {
		f.createSkip[path]--
		return false
	}
	return true
}

func (f *faults) count(opened bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if opened {
		f.opened++
	} else {
		f.closed++
	}
}

type faultFS struct {
	hostsetup.FS
	f *faults
}

func (x faultFS) OpenRootDir() (hostsetup.Dir, error) {
	if x.f.rootErr != nil {
		return nil, x.f.rootErr
	}
	d, err := x.FS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	x.f.count(true)
	return faultDir{d, x.f}, nil
}

type faultDir struct {
	hostsetup.Dir
	f *faults
}

func (d faultDir) Open(name string) (hostsetup.Dir, error) {
	if err := d.f.openErr[filepath.Join(d.Path(), name)]; err != nil {
		return nil, &fs.PathError{Op: "openat", Path: filepath.Join(d.Path(), name), Err: err}
	}
	c, err := d.Dir.Open(name)
	if err != nil {
		return nil, err
	}
	d.f.count(true)
	return faultDir{c, d.f}, nil
}

func (d faultDir) Close() error {
	d.f.count(false)
	return d.Dir.Close()
}

func (d faultDir) Names() ([]string, error) {
	if err := d.f.namesErr[d.Path()]; err != nil {
		return nil, &fs.PathError{Op: "getdents", Path: d.Path(), Err: err}
	}
	names, err := d.Dir.Names()
	slices.Sort(names)
	slices.Reverse(names)
	return names, err
}

func (d faultDir) CreateFile(name string, perm fs.FileMode) (hostsetup.File, error) {
	p := filepath.Join(d.Path(), name)
	if err := d.f.createErr[p]; err != nil && d.f.skipCreate(p) {
		return nil, &fs.PathError{Op: "openat", Path: p, Err: err}
	}
	file, err := d.Dir.CreateFile(name, perm)
	if err != nil {
		return nil, err
	}
	d.f.count(true)
	return faultFile{file, d.f, p}, nil
}

func (d faultDir) OpenFile(name string) (hostsetup.ReadFile, error) {
	p := filepath.Join(d.Path(), name)
	if err := d.f.openFile[p]; err != nil {
		return nil, &fs.PathError{Op: "openat", Path: p, Err: err}
	}
	file, err := d.Dir.OpenFile(name)
	if err != nil {
		return nil, err
	}
	d.f.count(true)
	return faultReadFile{file, d.f, p}, nil
}

type faultFile struct {
	hostsetup.File
	f    *faults
	path string
}

func (x faultFile) Write(p []byte) (int, error) {
	if err := x.f.writeErr[x.path]; err != nil {
		return 0, &fs.PathError{Op: "write", Path: x.path, Err: err}
	}
	return x.File.Write(p)
}

func (x faultFile) Sync() error {
	if err := x.f.fileErr[x.path]; err != nil {
		return &fs.PathError{Op: "fsync", Path: x.path, Err: err}
	}
	return x.File.Sync()
}

func (x faultFile) Close() error {
	x.f.count(false)
	return x.File.Close()
}

type faultReadFile struct {
	hostsetup.ReadFile
	f    *faults
	path string
}

func (x faultReadFile) Read(p []byte) (int, error) {
	if err := x.f.readErr[x.path]; err != nil {
		return 0, &fs.PathError{Op: "read", Path: x.path, Err: err}
	}
	return x.ReadFile.Read(p)
}

func (x faultReadFile) Close() error {
	x.f.count(false)
	return x.ReadFile.Close()
}

func (d faultDir) Mkdir(name string, perm fs.FileMode) (hostsetup.Dir, error) {
	d.f.mu.Lock()
	err := d.f.mkdirErr[filepath.Join(d.Path(), name)]
	d.f.mu.Unlock()
	if err != nil {
		return nil, &fs.PathError{Op: "mkdirat", Path: filepath.Join(d.Path(), name), Err: err}
	}
	c, err := d.Dir.Mkdir(name, perm)
	if err != nil {
		return nil, err
	}
	d.f.count(true)
	return faultDir{c, d.f}, nil
}

func (d faultDir) Remove(name string) error {
	d.f.mu.Lock()
	d.f.removed = append(d.f.removed, filepath.Join(d.Path(), name))
	d.f.ops = append(d.f.ops, "remove "+filepath.Join(d.Path(), name))
	err := d.f.removeErr[filepath.Join(d.Path(), name)]
	d.f.mu.Unlock()
	if err != nil {
		return &fs.PathError{Op: "unlinkat", Path: filepath.Join(d.Path(), name), Err: err}
	}
	return d.Dir.Remove(name)
}

func (d faultDir) RemoveDir(name string) error {
	d.f.mu.Lock()
	d.f.removed = append(d.f.removed, filepath.Join(d.Path(), name))
	d.f.ops = append(d.f.ops, "remove "+filepath.Join(d.Path(), name))
	err := d.f.removeDirErr[filepath.Join(d.Path(), name)]
	d.f.mu.Unlock()
	if err != nil {
		return &fs.PathError{Op: "unlinkat", Path: filepath.Join(d.Path(), name), Err: err}
	}
	return d.Dir.RemoveDir(name)
}

func (d faultDir) Move(oldName string, newDir hostsetup.Dir, newName string) error {
	if d.f.moveHook != nil {
		d.f.moveHook()
	}
	if d.f.moveErr != nil {
		return &fs.PathError{Op: "renameat", Path: newName, Err: d.f.moveErr}
	}
	return d.Dir.Move(oldName, newDir, newName)
}

func (d faultDir) Rename(oldName, newName string) error {
	if d.f.renameErr != nil {
		return &fs.PathError{Op: "renameat", Path: newName, Err: d.f.renameErr}
	}
	return d.Dir.Rename(oldName, newName)
}

func (d faultDir) Sync() error {
	d.f.mu.Lock()
	d.f.ops = append(d.f.ops, "sync "+d.Path())
	d.f.mu.Unlock()
	if err := d.f.syncErr[d.Path()]; err != nil {
		return err
	}
	return d.Dir.Sync()
}

func (d faultDir) Stat() (fs.FileInfo, error) {
	if err := d.f.statErr[d.Path()]; err != nil {
		return nil, &fs.PathError{Op: "fstat", Path: d.Path(), Err: err}
	}
	info, err := d.Dir.Stat()
	if dev, ok := d.f.device[d.Path()]; ok && err == nil {
		return onDevice{info, dev}, nil
	}
	return info, err
}

type onDevice struct {
	fs.FileInfo
	dev uint64
}

func (i onDevice) Sys() any {
	st := *i.FileInfo.Sys().(*syscall.Stat_t)
	st.Dev = i.dev
	return &st
}

// --- a restic repository

type fakeRepo struct {
	mu        sync.Mutex
	snapshots []restic.Snapshot
	listErr   error
	paths     map[string]bool // paths the snapshot holds
	pathErr   error
	// restore plays restic: it writes below req.Target.
	restore  func(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error)
	requests []restic.RestoreRequest
	listed   [][]string
}

func (r *fakeRepo) Snapshots(_ context.Context, tags []string) ([]restic.Snapshot, error) {
	r.listed = append(r.listed, tags)
	return r.snapshots, r.listErr
}

func (r *fakeRepo) HasPath(_ context.Context, _, path string) (bool, error) {
	return r.paths[path], r.pathErr
}

func (r *fakeRepo) RestoreTo(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.restore(ctx, req)
}

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

// snapshotF is the snapshot F of the scenarios: source s1.
func snapshotF() restic.Snapshot {
	return restic.Snapshot{ID: snapshotID, Time: time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), Tags: []string{"sard.source=s1", "sard.run=r1", "sard.step=st1"}}
}

// writesF plays a restic that restores the snapshot F.
func writesF(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
	if err := writeFile(filepath.Join(restoreRoot(req), "srv/data/a.txt"), "AAA", 0o640); err != nil {
		return restic.RestoreSummary{}, err
	}
	if err := writeFile(filepath.Join(restoreRoot(req), "srv/data/sub/b.txt"), "B", 0o644); err != nil {
		return restic.RestoreSummary{}, err
	}
	return restic.RestoreSummary{Files: 2, Bytes: 4}, nil
}

// --- the reporter

type event struct {
	phase             agentv1.StepPhase
	bytes, bytesTotal uint64
	files, filesTotal uint64
	level             agentv1.LogLevel
	text              string
	isLog             bool
}

type reporter struct {
	mu     sync.Mutex
	events []event
}

func (r *reporter) Progress(phase agentv1.StepPhase, b, bt uint64) {
	r.ProgressFiles(phase, b, bt, 0, 0)
}

func (r *reporter) ProgressFiles(phase agentv1.StepPhase, b, bt, f, ft uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{phase: phase, bytes: b, bytesTotal: bt, files: f, filesTotal: ft})
}

func (r *reporter) Log(level agentv1.LogLevel, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{isLog: true, level: level, text: text})
}

func (r *reporter) logs() []event {
	var out []event
	for _, e := range r.events {
		if e.isLog {
			out = append(out, e)
		}
	}
	return out
}

// --- the host

type host struct {
	t      *testing.T
	dir    string // D_R
	faults *faults
	engine *consolerestore.Engine
	repo   *fakeRepo
	rep    *reporter
	log    *bytes.Buffer
}

func newHost(t *testing.T) *host {
	t.Helper()
	base := t.TempDir()
	h := &host{t: t, dir: filepath.Join(base, "restore"), faults: &faults{}, log: &bytes.Buffer{}, rep: &reporter{}}
	h.repo = &fakeRepo{snapshots: []restic.Snapshot{snapshotF()}, paths: map[string]bool{}, restore: writesF}
	h.rebuild()
	return h
}

func (h *host) rebuild() {
	h.engine = consolerestore.New(consolerestore.Options{
		Dir:    h.dir,
		FS:     faultFS{hostsetup.OS{}, h.faults},
		Owner:  hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700},
		Logger: slog.New(slog.NewTextHandler(h.log, nil)),
	})
}

func (h *host) request() consolerestore.Request {
	return consolerestore.Request{
		CommandID: "c1", Repository: "main", SnapshotID: snapshotID, Source: "s1",
		Plan: sdk.RestorePlan{Destination: destRel},
	}
}

func (h *host) run(req consolerestore.Request) (consolerestore.Output, *consolerestore.Failure) {
	h.t.Helper()
	return h.engine.Restore(context.Background(), h.repo, req, h.rep)
}

func (h *host) dest() string { return filepath.Join(h.dir, destRel) }

// tree lists every entry below dir, with its mode for a directory, sorted.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, rel)
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func hasPrefix(t *testing.T, f *consolerestore.Failure, want string) {
	t.Helper()
	if f == nil {
		t.Fatalf("no failure, want one starting with %q", want)
	}
	if !strings.HasPrefix(f.Error(), want) {
		t.Fatalf("failure %q does not start with %q", f.Error(), want)
	}
}

func mentions(t *testing.T, text string, words ...string) {
	t.Helper()
	for _, w := range words {
		if !strings.Contains(text, w) {
			t.Errorf("%q does not mention %q", text, w)
		}
	}
}

// restoreRoot is where a restic that plays by the rules of the request writes:
// below root of the held staging directory, or below the target.
func restoreRoot(req restic.RestoreRequest) string {
	if req.Staging != nil {
		return filepath.Join(req.Staging.Name(), "root")
	}
	return req.Target
}

// heldPath is the staging directory restic inherits, "" if it gets none.
func heldPath(req restic.RestoreRequest) string {
	if req.Staging == nil {
		return ""
	}
	return req.Staging.Name()
}
