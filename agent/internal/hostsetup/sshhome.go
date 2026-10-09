// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// MaxSSHFileSize is the largest file of ~/.ssh a command reads (Р38).
const MaxSSHFileSize = 1 << 20

const sshDirName = ".ssh"

// Why a file of ~/.ssh is rejected, as the messages say it.
var (
	whyTooLarge = fmt.Sprintf("is larger than %d bytes", MaxSSHFileSize)
	whyLink     = "is a symbolic link: the agent does not follow links in the ssh directory"
)

// SSHHome is the ~/.ssh of the service user, held by its descriptor (Р37,
// Р38): root reads and writes there, and the service user can plant links,
// hard links and FIFOs in it, or swap the directory itself. So the home is
// walked from "/" without following a link, ~/.ssh is opened relative to
// it, and every read and write is relative to the descriptors held: no
// call gets a full path. What is not there yet is made only by Write.
type SSHHome struct {
	fsys FS
	user User
	// home and ssh are nil while the directory is not there.
	home, ssh Dir
}

// OpenSSHHome opens the home of the service user and its ~/.ssh as far as
// they exist; nothing is made. SSH_HOME_INVALID: the home of passwd is not
// an absolute path other than "/", or a link is on the way to it.
// SSH_FILE_REJECTED: ~/.ssh is a link, not a directory, or not the service
// user's or root's.
func OpenSSHHome(fsys FS, service User) (*SSHHome, *refusal.Failure) {
	if !usableHome(service.Home) {
		return nil, refusal.Fail(refusal.SSHHomeInvalid, "the home directory of %s in passwd is %q: it must be an absolute path other than /", service.Name, service.Home)
	}
	h := &SSHHome{fsys: fsys, user: service}
	home, err := h.walk(filepath.Clean(service.Home), true)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return h, nil
	case err != nil:
		return nil, refusal.Fail(refusal.SSHHomeInvalid, "the home directory of %s: %v", service.Name, err)
	}
	h.home = home
	if f := h.openSSH(); f != nil {
		h.Close()
		return nil, f
	}
	return h, nil
}

func usableHome(home string) bool {
	return filepath.IsAbs(home) && filepath.Clean(home) != "/"
}

// Close releases the descriptors; it may be called again.
func (h *SSHHome) Close() {
	for _, d := range []*Dir{&h.ssh, &h.home} {
		if *d != nil {
			_ = (*d).Close()
			*d = nil
		}
	}
}

// path is the full path of a name in ~/.ssh, for messages only.
func (h *SSHHome) path(name string) string {
	return filepath.Join(h.user.Home, sshDirName, name)
}

func (h *SSHHome) rejected(name, why string) *refusal.Failure {
	return refusal.Fail(refusal.SSHFileRejected, "%s %s", h.path(name), why)
}

// openSSH opens ~/.ssh if it exists.
func (h *SSHHome) openSSH() *refusal.Failure {
	ssh, err := h.home.Open(sshDirName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return h.rejected("", "cannot be used as the ssh directory: "+err.Error())
	}
	h.ssh = ssh
	return h.checkSSHOwner()
}

// checkSSHOwner: ~/.ssh belongs to the service user or root.
func (h *SSHHome) checkSSHOwner() *refusal.Failure {
	info, err := h.ssh.Stat()
	if err != nil {
		return h.rejected("", "cannot be looked at: "+err.Error())
	}
	if why := h.dirProblem(info); why != "" {
		return h.rejected("", why)
	}
	return nil
}

// dirProblem is why ~/.ssh is not acceptable: not the user's or root's, or
// writable by group or others.
func (h *SSHHome) dirProblem(info fs.FileInfo) string {
	if why := h.ownerProblem(info); why != "" {
		return why
	}
	return writableProblem(info)
}

// writableProblem: group or others may write to it, so any local user could
// plant a host key or a link there.
func writableProblem(info fs.FileInfo) string {
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Sprintf("is writable by group or others (mode %04o)", info.Mode().Perm())
	}
	return ""
}

// homeRuleError is a component of the path to the home that others could
// have tampered with.
type homeRuleError struct{ path, why string }

func (e *homeRuleError) Error() string { return e.path + " " + e.why }

// walk opens the directory at the path from "/" without following a link,
// judging each component by what it is before it is entered: the home must
// be the user's or root's and closed to group and others; each ancestor must
// be root's or the user's and closed to them, unless it has the sticky bit
// (/tmp). A missing component is fs.ErrNotExist, with its path.
func (h *SSHHome) walk(path string, isHome bool) (Dir, error) {
	cur, err := h.fsys.OpenRootDir()
	if err != nil {
		return nil, err
	}
	names := components(path)
	for i, name := range names {
		next, err := h.step(cur, name, isHome && i == len(names)-1)
		_ = cur.Close()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

func (h *SSHHome) step(cur Dir, name string, home bool) (Dir, error) {
	path := filepath.Join(cur.Path(), name)
	info, err := cur.Lstat(name)
	switch {
	case err != nil:
		return nil, err
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, &NotDirError{Path: path, Symlink: true}
	case !info.IsDir():
		return nil, &NotDirError{Path: path}
	}
	if why := h.componentProblem(info, home); why != "" {
		return nil, &homeRuleError{path: path, why: why}
	}
	return cur.Open(name)
}

// componentProblem: why a directory on the way to the home is not acceptable.
func (h *SSHHome) componentProblem(info fs.FileInfo, home bool) string {
	if why := h.ownerProblem(info); why != "" {
		return why
	}
	if !home && info.Mode()&fs.ModeSticky != 0 {
		return ""
	}
	return writableProblem(info)
}

// ownerProblem is why the owner of info is not acceptable; "" if it is.
func (h *SSHHome) ownerProblem(info fs.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "cannot be judged: no owner"
	}
	if st.Uid != h.user.UID && st.Uid != 0 {
		return fmt.Sprintf("is owned by uid %d: only the service user and root may own it", st.Uid)
	}
	return ""
}

// Read reads a file of ~/.ssh that is there; found is false for one that
// is not. A file that is not a regular file with one name, of the service
// user or root, within MaxSSHFileSize, is SSH_FILE_REJECTED and is not read.
func (h *SSHHome) Read(name string) (data []byte, found bool, f *refusal.Failure) {
	file, found, f := h.openRead(name)
	if !found {
		return nil, false, f
	}
	defer func() { _ = file.Close() }()
	data, why := h.readJudged(file)
	if why != "" {
		return nil, false, h.rejected(name, why)
	}
	return data, true, nil
}

// readJudged reads the file once its descriptor says it is acceptable; why
// is what is wrong with it.
func (h *SSHHome) readJudged(file ReadFile) (data []byte, why string) {
	info, err := file.Stat()
	if err != nil {
		return nil, "cannot be looked at: " + err.Error()
	}
	if why := h.fileProblem(info); why != "" {
		return nil, why
	}
	data, err = io.ReadAll(io.LimitReader(file, MaxSSHFileSize+1))
	if err != nil {
		return nil, "cannot be read: " + err.Error()
	}
	if len(data) > MaxSSHFileSize {
		return nil, whyTooLarge
	}
	return data, ""
}

// openRead opens a file of ~/.ssh for reading without following a link;
// found is false, with no failure, for one that is not there.
func (h *SSHHome) openRead(name string) (ReadFile, bool, *refusal.Failure) {
	if h.ssh == nil {
		return nil, false, nil
	}
	file, err := h.ssh.OpenFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case errors.Is(err, syscall.ELOOP):
		return nil, false, h.rejected(name, whyLink)
	case err != nil:
		return nil, false, h.rejected(name, "cannot be opened: "+err.Error())
	}
	return file, true, nil
}

// fileProblem is why a file is not acceptable; "" if it is.
func (h *SSHHome) fileProblem(info fs.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return whyLink
	case !info.Mode().IsRegular() || !ok:
		return "is not a regular file"
	case st.Nlink > 1:
		return "has more than one name (a hard link was planted?)"
	case info.Size() > MaxSSHFileSize:
		return whyTooLarge
	}
	return h.dirProblem(info)
}

// Present says whether a file of ~/.ssh is there, judging it by what it
// is without opening it: for the private key, which root never reads.
func (h *SSHHome) Present(name string) (bool, *refusal.Failure) {
	if h.ssh == nil {
		return false, nil
	}
	info, err := h.ssh.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, h.rejected(name, "cannot be looked at: "+err.Error())
	}
	if why := h.fileProblem(info); why != "" {
		return false, h.rejected(name, why)
	}
	return true, nil
}

// Write replaces a file of ~/.ssh with data, making the home and ~/.ssh
// first if they are not there: a temporary file made with O_EXCL in the
// held directory gets its owner and mode before its content, is synced,
// renamed in the same directory, and the directory is synced (Р4, Р38).
// The file is the service user's, 0600. CONFIG_WRITE names the file.
func (h *SSHHome) Write(name string, data []byte) *refusal.Failure {
	if f := h.Ensure(); f != nil {
		return f
	}
	err := h.writeAtomic(name, data)
	if err != nil {
		return refusal.Fail(refusal.ConfigWrite, "%v", err)
	}
	return nil
}

func (h *SSHHome) attrs(mode fs.FileMode) Attrs {
	return Attrs{UID: int(h.user.UID), GID: int(h.user.GID), Mode: mode}
}

// Ensure makes the home and ~/.ssh when they are missing: the last
// component of each inside the parent held, owner and mode through the
// descriptor. A missing parent of the home is CONFIG_WRITE naming it, and
// nothing is made.
func (h *SSHHome) Ensure() *refusal.Failure {
	if h.ssh != nil {
		return nil
	}
	if h.home == nil {
		if f := h.makeHome(); f != nil {
			return f
		}
	}
	ssh, err := openOrMake(h.home, sshDirName, h.attrs(0o700))
	if err != nil {
		return h.dirFailure(err)
	}
	h.ssh = ssh
	return h.checkSSHOwner()
}

func (h *SSHHome) makeHome() *refusal.Failure {
	home := filepath.Clean(h.user.Home)
	parent, err := h.walk(filepath.Dir(home), false)
	if err != nil {
		return h.dirFailure(err)
	}
	defer func() { _ = parent.Close() }()
	d, err := openOrMake(parent, filepath.Base(home), h.attrs(0o700))
	if err != nil {
		return h.dirFailure(err)
	}
	h.home = d
	return nil
}

// dirFailure is the failure of opening or making a directory.
func (h *SSHHome) dirFailure(err error) *refusal.Failure {
	var notDir *NotDirError
	var rule *homeRuleError
	if errors.As(err, &notDir) || errors.As(err, &rule) {
		return refusal.Fail(refusal.SSHHomeInvalid, "%v", err)
	}
	return refusal.Fail(refusal.ConfigWrite, "%v", err)
}

// openOrMake opens the directory called name in parent, making it first
// when it is not there.
func openOrMake(parent Dir, name string, a Attrs) (Dir, error) {
	d, err := parent.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return makeComponent(parent, name, a)
	}
	return d, err
}

// writeAtomic is the write of Write; its errors are *WriteError.
func (h *SSHHome) writeAtomic(name string, data []byte) error {
	final := h.path(name)
	tmp, f, err := h.createTemp(name)
	if err != nil {
		return &WriteError{Path: final, Op: "create", Err: err}
	}
	if err := fill(f, data, h.attrs(0o600)); err != nil {
		_ = f.Close()
		_ = h.ssh.Remove(tmp)
		return &WriteError{Path: final, Op: "write", Err: err}
	}
	if err := h.ssh.Rename(tmp, name); err != nil {
		_ = h.ssh.Remove(tmp)
		return &WriteError{Path: final, Op: "rename", Err: err}
	}
	if err := h.ssh.Sync(); err != nil {
		return &WriteError{Path: final, Op: "sync", Err: err}
	}
	return nil
}

// createTemp makes the temporary file of a write; a name that is taken is
// not reused (a planted file is never opened), another one is tried.
func (h *SSHHome) createTemp(name string) (string, File, error) {
	var err error
	for range 8 {
		var suffix [6]byte
		if _, err = rand.Read(suffix[:]); err != nil {
			return "", nil, err
		}
		tmp := "." + name + ".tmp-" + hex.EncodeToString(suffix[:])
		var f File
		if f, err = h.ssh.CreateFile(tmp, 0o600); err == nil {
			return tmp, f, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, err
}
