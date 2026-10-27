// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secretbind

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// maxFileBytes bounds a binding file: a plugin and a few targets.
const maxFileBytes = 64 << 10

// writeBits: a binding others may change is not the operator's word.
const writeBits = 0o022

// ReadFunc reads a file and says what it is, judged from the opened file.
type ReadFunc func(path string) ([]byte, secrets.Info, error)

// Store reads the bindings of the host. A file is read on every call, so a
// binding the operator changes applies to the next step.
type Store struct {
	path func(name string) string
	uid  uint32
	read ReadFunc
}

// NewStore returns the bindings in the files path names
// (hostsetup.Layout.SecretBinding), which root or the agent's uid own.
func NewStore(path func(name string) string, agentUID uint32, read ReadFunc) *Store {
	return &Store{path: path, uid: agentUID, read: read}
}

// Binding is the binding of the secret called name; ok is false when it has
// none. The error names the secret, never the file: it may leave the host.
func (s *Store) Binding(name string) (b Binding, ok bool, err error) {
	data, info, err := s.read(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return Binding{}, false, nil
	}
	if err == nil {
		err = s.judge(info)
	}
	if err == nil {
		b, err = Parse(data)
	}
	if err != nil {
		return Binding{}, false, fmt.Errorf("binding of secret %q: %w", name, withoutPath(err))
	}
	return b, true, nil
}

// judge: a regular file only root or the agent can change.
func (s *Store) judge(info secrets.Info) error {
	switch {
	case !info.Mode.IsRegular():
		return errors.New("not a regular file")
	case info.Mode.Perm()&writeBits != 0:
		return fmt.Errorf("the file has mode %v: it is writable by group or others", info.Mode.Perm())
	case info.UID != 0 && info.UID != s.uid:
		return fmt.Errorf("the file is owned by uid %d, not by root or the agent", info.UID)
	}
	return nil
}

// ReadFile is the ReadFunc of the host: the file is opened without following
// a link and without blocking, and judged from its descriptor.
func ReadFile(path string) ([]byte, secrets.Info, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NOCTTY|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, secrets.Info{}, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, secrets.Info{}, err
	}
	info := secrets.Info{Mode: st.Mode(), Size: st.Size()}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		info.UID, info.GID = sys.Uid, sys.Gid
	}
	if !st.Mode().IsRegular() {
		return nil, info, nil
	}
	data, err := readBounded(f)
	return data, info, err
}

// readBounded reads the whole file, or fails if it is longer than a binding.
func readBounded(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFileBytes+1))
	if err == nil && len(data) > maxFileBytes {
		return nil, fmt.Errorf("the file is larger than %d bytes", maxFileBytes)
	}
	return data, err
}

// withoutPath drops the path from a file error: only names leave the host.
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
