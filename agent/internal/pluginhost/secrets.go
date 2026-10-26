// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Secrets are the secrets defined on this host (config "secrets"): a name
// and the file holding the value, and the binding of each to a plugin and its
// targets (S11). Only names ever leave the host (ADR 0008).
type Secrets struct {
	files    map[string]string
	read     func(name string) ([]byte, error)
	bindings Bindings
}

// Bindings are the bindings of the secrets of the host (*secretbind.Store);
// ok is false for a secret without one.
type Bindings interface {
	Binding(name string) (b secretbind.Binding, ok bool, err error)
}

// NewSecrets returns the secrets of files (name → path), read with read and
// given only as bindings allow.
func NewSecrets(files map[string]string, read func(name string) ([]byte, error), bindings Bindings) *Secrets {
	return &Secrets{files: files, read: read, bindings: bindings}
}

// Has reports whether name is a defined secret.
func (s *Secrets) Has(name string) bool {
	_, ok := s.files[name]
	return ok
}

// SecretFor reads the value of the named secret for plugin to send to target,
// once its binding allows it: a secret it does not allow is a
// *sdk.BindingError and its file is not read.
func (s *Secrets) SecretFor(plugin, name string, target sdk.Target) (sdk.Secret, error) {
	grant, err := s.Allow(plugin, name, target)
	if err != nil {
		return sdk.Secret{}, err
	}
	value, err := s.value(name)
	if err != nil {
		return sdk.Secret{}, err
	}
	return sdk.Secret{Value: value, Grant: grant}, nil
}

// Allow is what the binding of the named secret allows plugin at target, or
// why it does not: an unknown name is a *sdk.SecretError, a secret without a
// binding or bound elsewhere a *sdk.BindingError. Nothing of the value is read.
func (s *Secrets) Allow(plugin, name string, target sdk.Target) (sdk.Grant, error) {
	if !s.Has(name) {
		return sdk.Grant{}, &sdk.SecretError{Name: name}
	}
	b, ok, err := s.bindings.Binding(name)
	switch {
	case err != nil:
		return sdk.Grant{}, err
	case !ok:
		return sdk.Grant{}, &sdk.BindingError{Name: name, Plugin: plugin, Target: target, Unbound: true}
	}
	if err := b.Allows(name, plugin, target); err != nil {
		return sdk.Grant{}, err
	}
	return b.Grant(), nil
}

// value reads the value of the named secret. The file is read on every call,
// so a rotated secret is picked up. Errors never contain the value.
func (s *Secrets) value(name string) ([]byte, error) {
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
