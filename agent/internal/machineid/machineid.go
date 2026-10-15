// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package machineid computes the identifier of the machine the agent sends in
// every Register (docs/specs/agent/agent-role.feature, С7–С11): the HMAC of
// the host's own machine-id, keyed by the tenant, so that the server can see
// that two agents share a host without being able to learn the host's real
// machine-id or to match hosts across tenants.
package machineid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	keyPrefix  = "sard-machine-id/v1"
	fileName   = "machine-id"
	randomSize = 32
	fileMode   = 0o600
	dirMode    = 0o700
)

// FS is the part of the file system the computation uses.
type FS interface {
	ReadFile(name string) ([]byte, error)
	MkdirAll(path string, perm fs.FileMode) error
	// WriteFileAtomic writes a temporary file and renames it over name.
	WriteFileAtomic(name string, data []byte, perm fs.FileMode) error
}

// Host is everything Compute reaches outside its arguments.
type Host struct {
	FS FS
	// Rand supplies the 32 random bytes of a generated identifier.
	Rand io.Reader
	// Sources are the host's machine-id files, in order of preference.
	Sources []string
	Logger  *slog.Logger
}

// DefaultSources are /etc/machine-id, then the D-Bus copy.
var DefaultSources = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// Compute returns hex(HMAC-SHA256(key = "sard-machine-id/v1" + tenantID,
// data = the host's machine id)). The id comes from the first source with
// content; with none, from <stateDir>/machine-id, generated once. A source
// that cannot be read, or a stored id that is not 64 lowercase hex digits,
// is an error naming the file; the stored file is never overwritten.
func Compute(h Host, stateDir, tenantID string) (string, error) {
	raw, err := h.rawID(stateDir)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(keyPrefix+tenantID))
	mac.Write([]byte(raw))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (h Host) rawID(stateDir string) (string, error) {
	for _, path := range h.Sources {
		data, err := h.FS.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return "", fmt.Errorf("machine id: cannot read %q: %w", path, err)
		}
		if id := strings.TrimSpace(string(data)); id != "" {
			return id, nil
		}
	}
	return h.stored(filepath.Join(stateDir, fileName))
}

// stored reads the generated identifier, or generates it when there is none.
func (h Host) stored(path string) (string, error) {
	data, err := h.FS.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return h.generate(path)
	case err != nil:
		return "", fmt.Errorf("machine id: cannot read %q: %w", path, err)
	case !validStored(data):
		return "", fmt.Errorf("machine id: %q must hold exactly 64 lowercase hexadecimal digits; remove the file to generate a new one", path)
	}
	return string(data), nil
}

func validStored(data []byte) bool {
	return len(data) == 2*randomSize && !slices.ContainsFunc(data, func(c byte) bool { return !isLowerHex(c) })
}

func isLowerHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }

func (h Host) generate(path string) (string, error) {
	buf := make([]byte, randomSize)
	if _, err := io.ReadFull(h.Rand, buf); err != nil {
		return "", fmt.Errorf("machine id: cannot generate the id for %q: %w", path, err)
	}
	id := hex.EncodeToString(buf)
	if err := h.FS.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return "", fmt.Errorf("machine id: cannot create the directory of %q: %w", path, err)
	}
	if err := h.FS.WriteFileAtomic(path, []byte(id), fileMode); err != nil {
		return "", fmt.Errorf("machine id: cannot write %q: %w", path, err)
	}
	h.Logger.Info("generated a random machine id", "path", path)
	return id, nil
}

// OS is the host's real file system, crypto/rand and /etc/machine-id.
func OS(logger *slog.Logger) Host {
	return Host{FS: osFS{}, Rand: rand.Reader, Sources: DefaultSources, Logger: logger}
}

type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error)         { return os.ReadFile(name) }
func (osFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

func (osFS) WriteFileAtomic(name string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-"+fileName+"-*")
	if err != nil {
		return err
	}
	if err := fill(tmp, data, perm); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// fill writes data to the new file, makes it durable and closes it.
func fill(tmp *os.File, data []byte, perm fs.FileMode) error {
	err := tmp.Chmod(perm)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	return errors.Join(err, tmp.Close())
}
