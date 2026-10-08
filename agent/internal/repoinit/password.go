// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"encoding/base64"
	"fmt"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"io"
	"os"
	"path/filepath"
)

// passwordBytes of randomness make the password (256 bits).
const passwordBytes = 32

// NewPassword reads 32 bytes from random (a cryptographic generator) and
// returns them as base64url without padding: 43 characters (В1).
func NewPassword(random io.Reader) (string, error) {
	raw := make([]byte, passwordBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", fmt.Errorf("generating a password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// passwordFile is what writeNew needs of a freshly created file.
type passwordFile interface {
	io.WriteCloser
	Chmod(mode os.FileMode) error
}

// WriteNew creates the file at path with mode 0600, whatever the umask,
// owned by the calling user, and writes data to it. It fails if the file
// exists and does not create directories. A failed write leaves no file.
func WriteNew(path string, data []byte) error {
	return writeNew(func(name string) (passwordFile, error) {
		return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}, path, data)
}

func writeNew(open func(name string) (passwordFile, error), path string, data []byte) error {
	f, err := open(path)
	if err != nil {
		return err
	}
	// The mode is set again: a umask may have removed owner bits.
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// CreatePassword generates a password and writes it, with a line break, to
// the password file of repo (В1). A write that fails is a *Failure; the
// directory is never created.
func CreatePassword(write func(path string, data []byte) error, random io.Reader, repo config.Repository) (string, error) {
	password, err := NewPassword(random)
	if err != nil {
		return "", err
	}
	if err := write(repo.PasswordFile, []byte(password+"\n")); err != nil {
		return "", refusal.Fail(refusal.PasswordFileWrite, "cannot create the password file %s in the directory %s: %v", repo.PasswordFile, filepath.Dir(repo.PasswordFile), err)
	}
	return password, nil
}
