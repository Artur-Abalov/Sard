// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"errors"
	"fmt"
	"slices"
)

// ErrEmptyName is returned when a plugin reports an empty Name.
var ErrEmptyName = errors.New("plugin has an empty name")

// ErrDuplicateName is returned when two plugins share a Name.
var ErrDuplicateName = errors.New("duplicate plugin name")

// Registry holds plugins by name.
type Registry struct {
	plugins map[string]Plugin
}

// NewRegistry builds a registry, rejecting empty and duplicate names.
func NewRegistry(plugins ...Plugin) (*Registry, error) {
	r := &Registry{plugins: make(map[string]Plugin, len(plugins))}
	for _, p := range plugins {
		name := p.Name()
		if name == "" {
			return nil, ErrEmptyName
		}
		if _, ok := r.plugins[name]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		r.plugins[name] = p
	}
	return r, nil
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
