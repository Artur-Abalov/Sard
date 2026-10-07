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
}

func newFakeFS() *fakeFS { return &fakeFS{owners: map[string]ownerRec{}} }

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

func (f *fakeFS) Mkdir(path string, perm os.FileMode) error {
	if err := f.fails("mkdir", path); err != nil {
		return err
	}
	return f.OS.Mkdir(path, perm)
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

func (f *fakeFS) Chown(path string, uid, gid int) error {
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
	if err := f.fails("remove", path); err != nil {
		return err
	}
	return f.OS.Remove(path)
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
