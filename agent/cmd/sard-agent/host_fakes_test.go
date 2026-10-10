// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// The fakes of the host-setup tests: the file system of a temporary
// directory with owner changes recorded instead of made (a test is not
// root) and any step made to fail; systemd, the system log and the
// terminal record what they are asked.

type ownerRec struct{ uid, gid int }

// fakeFS is hostsetup.OS that records chown and can fail a step.
type fakeFS struct {
	hostsetup.OS
	mu     sync.Mutex
	owners map[string]ownerRec
	// failOn is the operation that fails ("chown", "chmod", "sync",
	// "rename", "createtemp", "mkdir", "syncdir", "readdir"); failPath
	// limits it to paths that contain it.
	failOn, failPath string
	// failChownAt, if not zero, makes the owner change with this number
	// (counted from 1, all paths) fail.
	failChownAt int
	chownCalls  int
	// events lists the owner changes, renames and temporary files, in order.
	events []string
	// byPath lists the operations that were given a full path (open, chown,
	// rename, remove, link): the ssh directory is never touched this way.
	byPath []string
	// opened lists the files opened for reading through a held directory.
	opened []string
	// defaultUID is the owner a file nobody changed seems to have.
	defaultUID uint32
}

// pathCall notes an operation on a full path.
func (f *fakeFS) pathCall(op, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byPath = append(f.byPath, op+" "+path)
}

// pathCallsIn are the operations on full paths inside dir.
func (f *fakeFS) pathCallsIn(dir string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var got []string
	for _, c := range f.byPath {
		if _, path, _ := strings.Cut(c, " "); strings.HasPrefix(path, dir+"/") || path == dir {
			got = append(got, c)
		}
	}
	return got
}

func newFakeFS() *fakeFS { return &fakeFS{owners: map[string]ownerRec{}, defaultUID: serviceUID} }

var errFakeFailure = errors.New("injected failure")

func (f *fakeFS) fails(op, path string) error {
	if f.failOn == op && strings.Contains(path, f.failPath) {
		return &fs.PathError{Op: op, Path: path, Err: errFakeFailure}
	}
	return nil
}

func (f *fakeFS) setOwner(path string, uid, gid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners[path] = ownerRec{uid, gid}
	f.events = append(f.events, fmt.Sprintf("chown %s %d:%d", path, uid, gid))
}

func (f *fakeFS) ownerOf(path string) (ownerRec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.owners[path]
	return o, ok
}

// chowns counts the owner changes made so far.
func (f *fakeFS) chowns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if strings.HasPrefix(e, "chown ") {
			n++
		}
	}
	return n
}

func (f *fakeFS) CreateTemp(dir, pattern string) (hostsetup.File, error) {
	f.pathCall("createtemp", dir)
	if err := f.fails("createtemp", dir); err != nil {
		return nil, err
	}
	file, err := f.OS.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.events = append(f.events, "createtemp "+file.Name())
	f.mu.Unlock()
	return &fakeFile{File: file, fsys: f}, nil
}

type fakeFile struct {
	hostsetup.File
	fsys *fakeFS
}

func (f *fakeFile) Chown(uid, gid int) error {
	if err := f.fsys.fails("chown", f.Name()); err != nil {
		return err
	}
	if err := f.fsys.nthChown(f.Name()); err != nil {
		return err
	}
	f.fsys.setOwner(f.Name(), uid, gid)
	return nil
}

func (f *fakeFile) Chmod(m os.FileMode) error {
	if err := f.fsys.fails("chmod", f.Name()); err != nil {
		return err
	}
	return f.File.Chmod(m)
}

func (f *fakeFile) Sync() error {
	if err := f.fsys.fails("sync", f.Name()); err != nil {
		return err
	}
	return f.File.Sync()
}

func (f *fakeFS) Rename(oldpath, newpath string) error {
	f.pathCall("rename", newpath)
	if err := f.fails("rename", newpath); err != nil {
		return err
	}
	if err := f.OS.Rename(oldpath, newpath); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.owners[oldpath]; ok {
		f.owners[newpath] = o
		delete(f.owners, oldpath)
	}
	f.events = append(f.events, "rename "+oldpath+" "+newpath)
	return nil
}

func (f *fakeFS) SyncDir(dir string) error {
	if err := f.fails("syncdir", dir); err != nil {
		return err
	}
	return f.OS.SyncDir(dir)
}

// nthChown fails the owner change that failChownAt names.
func (f *fakeFS) nthChown(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chownCalls++
	if f.failChownAt != 0 && f.chownCalls == f.failChownAt {
		return &fs.PathError{Op: "chown", Path: path, Err: errFakeFailure}
	}
	return nil
}

// chownFile records the owner of an open file by its name.
func (f *fakeFS) chownFile(file *os.File, uid, gid int) error {
	path := file.Name()
	if err := f.fails("chown", path); err != nil {
		return err
	}
	if err := f.nthChown(path); err != nil {
		return err
	}
	f.setOwner(path, uid, gid)
	return nil
}

func (f *fakeFS) Chown(path string, uid, gid int) error {
	f.pathCall("chown", path)
	if err := f.fails("chown", path); err != nil {
		return err
	}
	if err := f.nthChown(path); err != nil {
		return err
	}
	f.setOwner(path, uid, gid)
	return nil
}

func (f *fakeFS) Remove(path string) error {
	f.pathCall("remove", path)
	if err := f.fails("remove", path); err != nil {
		return err
	}
	return f.OS.Remove(path)
}

func (f *fakeFS) Link(oldpath, newpath string) error {
	f.pathCall("link", newpath)
	if err := f.fails("link", newpath); err != nil {
		return err
	}
	if err := f.OS.Link(oldpath, newpath); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.owners[oldpath]; ok {
		f.owners[newpath] = o
	}
	f.events = append(f.events, "link "+oldpath+" "+newpath)
	return nil
}

func (f *fakeFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return fakeDir{Dir: d, fsys: f}, nil
}

// fakeDir records the owner changes and the creations of a walk.
type fakeDir struct {
	hostsetup.Dir
	fsys *fakeFS
}

func (d fakeDir) wrap(next hostsetup.Dir, err error) (hostsetup.Dir, error) {
	if err != nil {
		return nil, err
	}
	return fakeDir{Dir: next, fsys: d.fsys}, nil
}

func (d fakeDir) Open(name string) (hostsetup.Dir, error) { return d.wrap(d.Dir.Open(name)) }

func (d fakeDir) Mkdir(name string, perm os.FileMode) (hostsetup.Dir, error) {
	if err := d.fsys.fails("mkdir", filepath.Join(d.Path(), name)); err != nil {
		return nil, err
	}
	return d.wrap(d.Dir.Mkdir(name, perm))
}

func (d fakeDir) Chown(uid, gid int) error {
	if err := d.fsys.fails("chown", d.Path()); err != nil {
		return err
	}
	if err := d.fsys.nthChown(d.Path()); err != nil {
		return err
	}
	d.fsys.setOwner(d.Path(), uid, gid)
	return nil
}

// CreateFile makes the file for real and records its owner instead of
// changing it; any step can be made to fail.
func (d fakeDir) CreateFile(name string, perm os.FileMode) (hostsetup.File, error) {
	path := filepath.Join(d.Path(), name)
	if err := d.fsys.fails("createtemp", path); err != nil {
		return nil, err
	}
	file, err := d.Dir.CreateFile(name, perm)
	if err != nil {
		return nil, err
	}
	d.fsys.mu.Lock()
	d.fsys.events = append(d.fsys.events, "dir-create "+path)
	d.fsys.mu.Unlock()
	return &fakeDirFile{File: file, fsys: d.fsys}, nil
}

// fakeDirFile is a file made through a held directory.
type fakeDirFile struct {
	hostsetup.File
	fsys *fakeFS
}

func (f *fakeDirFile) Chown(uid, gid int) error {
	if err := f.fsys.fails("chown", f.Name()); err != nil {
		return err
	}
	if err := f.fsys.nthChown(f.Name()); err != nil {
		return err
	}
	f.fsys.setOwner(f.Name(), uid, gid)
	return nil
}

func (f *fakeDirFile) Chmod(m os.FileMode) error {
	if err := f.fsys.fails("chmod", f.Name()); err != nil {
		return err
	}
	return f.File.Chmod(m)
}

func (f *fakeDirFile) Write(p []byte) (int, error) {
	if err := f.fsys.fails("write", f.Name()); err != nil {
		return 0, err
	}
	return f.File.Write(p)
}

func (f *fakeDirFile) Sync() error {
	if err := f.fsys.fails("sync", f.Name()); err != nil {
		return err
	}
	return f.File.Sync()
}

// seen is info as the recorded owner makes it look (a test is not root).
func (f *fakeFS) seen(path string, info fs.FileInfo, err error) (fs.FileInfo, error) {
	if err != nil {
		return nil, err
	}
	uid := f.defaultUID
	if o, ok := f.ownerOf(path); ok {
		uid = uint32(o.uid)
	}
	return ownedInfo{FileInfo: info, uid: uid}, nil
}

type ownedInfo struct {
	fs.FileInfo
	uid uint32
}

func (i ownedInfo) Sys() any {
	st := *i.FileInfo.Sys().(*syscall.Stat_t)
	st.Uid = i.uid
	return &st
}

// Stat shows the owner of ~/.ssh as recorded; other directories are left
// alone (os.SameFile needs the real FileInfo).
func (d fakeDir) Stat() (fs.FileInfo, error) {
	info, err := d.Dir.Stat()
	if filepath.Base(d.Path()) != ".ssh" {
		return info, err
	}
	return d.fsys.seen(d.Path(), info, err)
}

func (d fakeDir) OpenFile(name string) (hostsetup.ReadFile, error) {
	path := filepath.Join(d.Path(), name)
	d.fsys.mu.Lock()
	d.fsys.opened = append(d.fsys.opened, path)
	d.fsys.mu.Unlock()
	f, err := d.Dir.OpenFile(name)
	if err != nil {
		return nil, err
	}
	return fakeRead{ReadFile: f, path: path, fsys: d.fsys}, nil
}

type fakeRead struct {
	hostsetup.ReadFile
	path string
	fsys *fakeFS
}

func (r fakeRead) Stat() (fs.FileInfo, error) {
	info, err := r.ReadFile.Stat()
	return r.fsys.seen(r.path, info, err)
}

func (d fakeDir) Lstat(name string) (fs.FileInfo, error) {
	info, err := d.Dir.Lstat(name)
	return d.fsys.seen(filepath.Join(d.Path(), name), info, err)
}

func (d fakeDir) Rename(oldName, newName string) error {
	oldPath, newPath := filepath.Join(d.Path(), oldName), filepath.Join(d.Path(), newName)
	if err := d.fsys.fails("rename", newPath); err != nil {
		return err
	}
	if err := d.Dir.Rename(oldName, newName); err != nil {
		return err
	}
	d.fsys.mu.Lock()
	defer d.fsys.mu.Unlock()
	if o, ok := d.fsys.owners[oldPath]; ok {
		d.fsys.owners[newPath] = o
		delete(d.fsys.owners, oldPath)
	}
	d.fsys.events = append(d.fsys.events, "dir-rename "+oldPath+" "+newPath)
	return nil
}

func (d fakeDir) Sync() error {
	if err := d.fsys.fails("syncdir", d.Path()); err != nil {
		return err
	}
	return d.Dir.Sync()
}

func (f *fakeFS) OpenRoot(path string) (hostsetup.Root, error) {
	f.pathCall("openroot", path)
	root, err := f.OS.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	return &fakeRoot{Root: root, base: path, fsys: f}, nil
}

// fakeRoot records the owner changes made inside a Root.
type fakeRoot struct {
	hostsetup.Root
	base string
	fsys *fakeFS
}

func (r *fakeRoot) Lchown(name string, uid, gid int) error {
	full := filepath.Join(r.base, name)
	if err := r.fsys.fails("chown", full); err != nil {
		return err
	}
	if err := r.fsys.nthChown(full); err != nil {
		return err
	}
	r.fsys.setOwner(full, uid, gid)
	return nil
}

func (f *fakeFS) ReadDir(path string) ([]fs.DirEntry, error) {
	if err := f.fails("readdir", path); err != nil {
		return nil, err
	}
	return f.OS.ReadDir(path)
}

// ownerStat is the stat of a host whose owner changes are the recorded
// ones; a file nobody changed belongs to the service user.
func (f *fakeFS) ownerStat(serviceUID int) secrets.StatFunc {
	return func(path string) (secrets.Info, error) {
		info, err := secrets.RealStat(path)
		if err != nil {
			return info, err
		}
		info.UID = uint32(serviceUID)
		if o, ok := f.ownerOf(path); ok {
			info.UID = uint32(o.uid)
		}
		return info, nil
	}
}

// fakeSystemd records what it is asked.
type fakeSystemd struct {
	present, active bool
	restartErr      error
	calls           []string
}

func (s *fakeSystemd) Present() bool { return s.present }

func (s *fakeSystemd) Active(unit string) (bool, error) {
	s.calls = append(s.calls, "is-active "+unit)
	return s.active, nil
}

func (s *fakeSystemd) Restart(unit string) error {
	s.calls = append(s.calls, "restart "+unit)
	return s.restartErr
}

func (s *fakeSystemd) DaemonReload() error {
	s.calls = append(s.calls, "daemon-reload")
	return nil
}

// touched says whether any systemctl call was made.
func (s *fakeSystemd) touched() bool { return len(s.calls) > 0 }

// restarted says whether a restart was asked for.
func (s *fakeSystemd) restarted() bool {
	for _, c := range s.calls {
		if strings.HasPrefix(c, "restart") {
			return true
		}
	}
	return false
}

// fakeSyslog takes the audit lines.
type fakeSyslog struct {
	lines []string
	down  bool
}

func (s *fakeSyslog) open() (hostsetup.Auditor, error) {
	if s.down {
		return nil, errors.New("no /dev/log")
	}
	return s, nil
}

func (s *fakeSyslog) Write(line string) error {
	s.lines = append(s.lines, line)
	return nil
}

// fakeTerminal is the operator at the terminal: it answers prompts from a
// script and notes whether it was ever asked.
type fakeTerminal struct {
	answers []string
	prompts []string
}

func (t *fakeTerminal) ReadSecret(prompt string) ([]byte, error) {
	t.prompts = append(t.prompts, prompt)
	if len(t.answers) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	a := t.answers[0]
	t.answers = t.answers[1:]
	return []byte(a), nil
}

// ReadLine is a question whose answer is seen (the confirmation of a host
// key); it takes the next answer of the script like ReadSecret.
func (t *fakeTerminal) ReadLine(prompt string) ([]byte, error) { return t.ReadSecret(prompt) }

// hungInput is a pipe nobody ever closes: any read is a failure of the command.
type hungInput struct{ reads int }

func (h *hungInput) Read([]byte) (int, error) {
	h.reads++
	return 0, errors.New("the command read standard input")
}

// users is the lookup of a host with the given users.
func users(list ...hostsetup.User) hostsetup.LookupFunc {
	return func(name string) (hostsetup.User, error) {
		for _, u := range list {
			if u.Name == name {
				return u, nil
			}
		}
		return hostsetup.User{}, hostsetup.ErrNoUser
	}
}

// tree is every file and directory under dir with its mode, owner (as
// recorded) and content: the host, for "the host is unchanged".
func tree(root string, fsys *fakeFS) (map[string]string, error) {
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			content = string(data)
		}
		owner := "-"
		if o, ok := fsys.ownerOf(p); ok {
			owner = fmt.Sprintf("%d:%d", o.uid, o.gid)
		}
		got[p] = fmt.Sprintf("%v %s %q", info.Mode(), owner, content)
		return nil
	})
	return got, err
}

// diff lists the paths that differ between two trees.
func diff(before, after map[string]string) []string {
	var out []string
	for p, v := range before {
		if got, ok := after[p]; !ok || got != v {
			out = append(out, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			out = append(out, p+" (new)")
		}
	}
	sort.Strings(out)
	return out
}
