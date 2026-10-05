// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Secrets are the secrets defined on this host (config "secrets"): a name
// and the file holding the value. Only names ever leave the host (ADR 0008).
type Secrets struct {
	files map[string]string
	read  func(name string) ([]byte, error)
}

// NewSecrets returns the secrets of files (name → path), read with read.
func NewSecrets(files map[string]string, read func(name string) ([]byte, error)) *Secrets {
	return &Secrets{files: files, read: read}
}

// Has reports whether name is a defined secret.
func (s *Secrets) Has(name string) bool {
	_, ok := s.files[name]
	return ok
}

// Secret reads the value of the named secret. The file is read on every
// call, so a rotated secret is picked up. Errors never contain the value.
func (s *Secrets) Secret(name string) ([]byte, error) {
	path, ok := s.files[name]
	if !ok {
		return nil, &sdk.SecretError{Name: name}
	}
	value, err := s.read(path)
	if err != nil {
		return nil, fmt.Errorf("secret %q: %w", name, withoutPath(err))
	}
	return value, nil
}

// withoutPath drops the path from a file error: only names leave the host
// (ADR 0008).
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
