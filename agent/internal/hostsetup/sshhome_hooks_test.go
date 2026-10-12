// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// hooks drive the real file system of a test through wrappers that record
// the operations of the descriptors, can fail a step, and can make a file
// look as if another user owned it (a test is not root and cannot chown).
type hooks struct {
	mu sync.Mutex
	// ops lists what was done, in order: "create <path>", "chown <path>",
	// "chmod <path>", "write <path>", "sync <path>", "rename <old> <new>",
	// "remove <path>", "openfile <path>", "lstat <path>".
	ops []string
	// uid is the owner a name seems to have to Stat (by base name).
	uid map[string]uint32
	// fail is the step that fails: "chown", "chmod", "write", "sync", "rename",
	// "dirsync", "lstat", "dirstat", "mkdir", "open" for the path (base name)
	// it names.
	fail map[string]string
	// size is the size a name seems to have to Stat (by base name).
	size map[string]int64
	// anonymous are the names whose owner Stat cannot tell.
	anonymous map[string]bool
	// createTries are the names CreateFile was asked for; createErr is
	// what it fails with, when set.
	createTries []string
	createErr   error
}

func newHooks() *hooks {
	return &hooks{uid: map[string]uint32{}, fail: map[string]string{}, size: map[string]int64{}, anonymous: map[string]bool{}}
}

func (h *hooks) note(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ops = append(h.ops, sprintf(format, args...))
}

func (h *hooks) failing(step, path string) error {
	if h.fail[step] != "" && strings.Contains(path, h.fail[step]) {
		return &fs.PathError{Op: step, Path: path, Err: errInjected}
	}
	return nil
}

func (h *hooks) has(prefix string) bool {
	for _, o := range h.ops {
		if strings.HasPrefix(o, prefix) {
			return true
		}
	}
	return false
}

func (h *hooks) fs() hostsetup.FS { return sshHookFS{h: h} }

type sshHookFS struct {
	hostsetup.OS
	h *hooks
}

func (f sshHookFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.OS.OpenRootDir()
	if err != nil {
		return nil, err
	}
	return sshHookDir{Dir: d, h: f.h}, nil
}

type sshHookDir struct {
	hostsetup.Dir
	h *hooks
}

func (d sshHookDir) wrap(next hostsetup.Dir, err error) (hostsetup.Dir, error) {
	if err != nil {
		return nil, err
	}
	return sshHookDir{Dir: next, h: d.h}, nil
}

func (d sshHookDir) Open(name string) (hostsetup.Dir, error) { return d.wrap(d.Dir.Open(name)) }
func (d sshHookDir) Mkdir(name string, perm os.FileMode) (hostsetup.Dir, error) {
	if err := d.h.failing("mkdir", name); err != nil {
		return nil, err
	}
	return d.wrap(d.Dir.Mkdir(name, perm))
}

func (d sshHookDir) Stat() (fs.FileInfo, error) {
	if err := d.h.failing("dirstat", filepath.Base(d.Path())); err != nil {
		return nil, err
	}
	info, err := d.Dir.Stat()
	return d.h.seen(filepath.Base(d.Path()), info, err)
}

func (d sshHookDir) CreateFile(name string, perm fs.FileMode) (hostsetup.File, error) {
	d.h.mu.Lock()
	d.h.createTries = append(d.h.createTries, name)
	d.h.mu.Unlock()
	if d.h.createErr != nil {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: d.h.createErr}
	}
	f, err := d.Dir.CreateFile(name, perm)
	if err != nil {
		return nil, err
	}
	d.h.note("create %s", f.Name())
	return sshHookFile{File: f, h: d.h}, nil
}

func (d sshHookDir) OpenFile(name string) (hostsetup.ReadFile, error) {
	d.h.note("openfile %s", filepath.Join(d.Path(), name))
	if err := d.h.failing("open", name); err != nil {
		return nil, err
	}
	f, err := d.Dir.OpenFile(name)
	if err != nil {
		return nil, err
	}
	return sshHookRead{ReadFile: f, name: name, h: d.h}, nil
}

func (d sshHookDir) Lstat(name string) (fs.FileInfo, error) {
	d.h.note("lstat %s", filepath.Join(d.Path(), name))
	if err := d.h.failing("lstat", name); err != nil {
		return nil, err
	}
	info, err := d.Dir.Lstat(name)
	return d.h.seen(name, info, err)
}

func (d sshHookDir) Rename(oldName, newName string) error {
	if err := d.h.failing("rename", newName); err != nil {
		return err
	}
	d.h.note("rename %s %s", filepath.Join(d.Path(), oldName), filepath.Join(d.Path(), newName))
	return d.Dir.Rename(oldName, newName)
}

func (d sshHookDir) Remove(name string) error {
	d.h.note("remove %s", filepath.Join(d.Path(), name))
	return d.Dir.Remove(name)
}

func (d sshHookDir) Sync() error {
	if err := d.h.failing("dirsync", d.Path()); err != nil {
		return err
	}
	d.h.note("dirsync %s", d.Path())
	return d.Dir.Sync()
}

type sshHookFile struct {
	hostsetup.File
	h *hooks
}

func (f sshHookFile) Chown(uid, gid int) error {
	if err := f.h.failing("chown", f.Name()); err != nil {
		return err
	}
	f.h.note("chown %s", f.Name())
	return f.File.Chown(uid, gid)
}

func (f sshHookFile) Chmod(m os.FileMode) error {
	if err := f.h.failing("chmod", f.Name()); err != nil {
		return err
	}
	f.h.note("chmod %s", f.Name())
	return f.File.Chmod(m)
}

func (f sshHookFile) Write(p []byte) (int, error) {
	if err := f.h.failing("write", f.Name()); err != nil {
		return 0, err
	}
	f.h.note("write %s", f.Name())
	return f.File.Write(p)
}

func (f sshHookFile) Sync() error {
	if err := f.h.failing("sync", f.Name()); err != nil {
		return err
	}
	f.h.note("sync %s", f.Name())
	return f.File.Sync()
}

type sshHookRead struct {
	hostsetup.ReadFile
	name string
	h    *hooks
}

func (r sshHookRead) Read(p []byte) (int, error) {
	if err := r.h.failing("read", r.name); err != nil {
		return 0, err
	}
	return r.ReadFile.Read(p)
}

func (r sshHookRead) Stat() (fs.FileInfo, error) {
	if err := r.h.failing("stat", r.name); err != nil {
		return nil, err
	}
	info, err := r.ReadFile.Stat()
	return r.h.seen(r.name, info, err)
}

// seen is info as the owner, size and owner-ship set for name make it look.
func (h *hooks) seen(name string, info fs.FileInfo, err error) (fs.FileInfo, error) {
	uid, hasUID := h.uid[name]
	size, hasSize := h.size[name]
	if err != nil || (!hasUID && !hasSize && !h.anonymous[name]) {
		return info, err
	}
	return seenInfo{FileInfo: info, uid: uid, hasUID: hasUID, size: size, hasSize: hasSize, anonymous: h.anonymous[name]}, nil
}

type seenInfo struct {
	fs.FileInfo
	uid       uint32
	hasUID    bool
	size      int64
	hasSize   bool
	anonymous bool
}

func (i seenInfo) Size() int64 {
	if i.hasSize {
		return i.size
	}
	return i.FileInfo.Size()
}

func (i seenInfo) Sys() any {
	if i.anonymous {
		return nil
	}
	st := *i.FileInfo.Sys().(*syscall.Stat_t)
	if i.hasUID {
		st.Uid = i.uid
	}
	return &st
}

// stepsOf are the kinds of the operations on the temporary file of name in
// dir, the rename of it onto name, and the sync of the directory, in order.
func (h *hooks) stepsOf(dir, name string) []string {
	tmp := dir + "/." + name + ".tmp-"
	var kinds []string
	for _, op := range h.ops {
		kind, rest, _ := strings.Cut(op, " ")
		if kind == "rename" && (!strings.HasPrefix(rest, tmp) || !strings.HasSuffix(rest, " "+dir+"/"+name)) {
			continue
		}
		if kind == "dirsync" || strings.HasPrefix(rest, tmp) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}
