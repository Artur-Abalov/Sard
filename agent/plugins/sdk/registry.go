// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// Limits of sard-server's Register (S4a, SnapshotRules.kt): a plugin set
// outside them is rejected, and the agent cannot connect.
const (
	MaxPlugins           = 64
	MaxConfigSchemaBytes = 64 * 1024
)

var (
	nameFormat    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	versionFormat = regexp.MustCompile(`^[!-~]{1,64}$`)
)

// Errors of NewRegistry.
var (
	ErrEmptyName      = errors.New("plugin has an empty name")
	ErrInvalidName    = errors.New("invalid plugin name")
	ErrDuplicateName  = errors.New("duplicate plugin name")
	ErrInvalidVersion = errors.New("invalid version")
	ErrInvalidSchema  = fmt.Errorf("config schema is not one JSON value of at most %d bytes without NUL characters", MaxConfigSchemaBytes)
	ErrTooManyPlugins = fmt.Errorf("more than %d plugins", MaxPlugins)
)

// Registry holds plugins by name.
type Registry struct {
	plugins map[string]Plugin
}

// NewRegistry builds a registry of plugins that sard-server's Register
// accepts: at most MaxPlugins, unique names of the server's name format,
// versions of 1 to 64 printable ASCII characters and config schemas that
// are one JSON value of at most MaxConfigSchemaBytes.
func NewRegistry(plugins ...Plugin) (*Registry, error) {
	if len(plugins) > MaxPlugins {
		return nil, ErrTooManyPlugins
	}
	r := &Registry{plugins: make(map[string]Plugin, len(plugins))}
	for _, p := range plugins {
		if err := check(p); err != nil {
			return nil, err
		}
		if _, ok := r.plugins[p.Name()]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, p.Name())
		}
		r.plugins[p.Name()] = p
	}
	return r, nil
}

func check(p Plugin) error {
	name := p.Name()
	switch {
	case name == "":
		return ErrEmptyName
	case !nameFormat.MatchString(name):
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	case !versionFormat.MatchString(p.Version()):
		return fmt.Errorf("plugin %q: %w: %q", name, ErrInvalidVersion, p.Version())
	case !storableSchema(p.ConfigSchema()):
		return fmt.Errorf("plugin %q: %w", name, ErrInvalidSchema)
	}
	return nil
}

// storableSchema mirrors the server: one JSON value, no NUL character,
// which PostgreSQL's jsonb cannot hold.
func storableSchema(schema []byte) bool {
	return len(schema) <= MaxConfigSchemaBytes && json.Valid(schema) && !bytes.Contains(schema, []byte(`\u0000`))
}

// Get returns the plugin registered under name.
func (r *Registry) Get(name string) (Plugin, bool) {
	p, ok := r.plugins[name]
	return p, ok
}

// Names returns all plugin names in lexical order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
