// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// The built-in retention floor (decision ДL1): the latest 7 snapshots of a
// source and every snapshot of the last 14 days.
const (
	BuiltInFloorKeepLast       = 7
	BuiltInFloorKeepWithinDays = 14
)

// ErrInvalidRetentionFloor is returned for a retention_floor setting the
// agent cannot use.
var ErrInvalidRetentionFloor = errors.New("invalid retention floor")

// FloorOrigin says where the floor of a repository comes from.
type FloorOrigin string

// The origins; the strings are the ones the log of the agent shows.
const (
	FloorBuiltIn          FloorOrigin = "built_in"
	FloorConfigDefault    FloorOrigin = "config_default"
	FloorConfigRepository FloorOrigin = "config_repository"
)

// Floor is the least the host keeps of every source in a repository when it
// forgets snapshots (КL1, КL8).
type Floor struct {
	Disabled       bool
	KeepLast       uint32
	KeepWithinDays uint32
	Origin         FloorOrigin
}

// RetentionFloor is the retention_floor section of the main config. Only the
// operator of the host sets it: a fragment of agent.d and a repository
// definition cannot (КL2).
type RetentionFloor struct {
	// Default applies to every repository of the host.
	Default *floorSettings
	// Repositories are the settings by repository name.
	Repositories map[string]floorSettings
}

// floorSettings are the keys of one floor section; nil is not set.
type floorSettings struct {
	keepLast       *uint32
	keepWithinDays *uint32
	disabled       bool
}

// UnmarshalYAML reads the section with errors that name the full key.
func (f *RetentionFloor) UnmarshalYAML(n *yaml.Node) error {
	return decodeMap(n, "retention_floor", func(key string, v *yaml.Node) error {
		switch key {
		case "default":
			s, err := readSettings(v, "retention_floor.default", false)
			f.Default = &s
			return err
		case "repositories":
			return f.readRepositories(v)
		}
		return keyError("retention_floor." + key)
	})
}

func (f *RetentionFloor) readRepositories(n *yaml.Node) error {
	f.Repositories = map[string]floorSettings{}
	return decodeMap(n, "retention_floor.repositories", func(name string, v *yaml.Node) error {
		s, err := readSettings(v, "retention_floor.repositories."+name, true)
		f.Repositories[name] = s
		return err
	})
}

func readSettings(n *yaml.Node, path string, allowDisabled bool) (floorSettings, error) {
	var s floorSettings
	err := decodeMap(n, path, func(key string, v *yaml.Node) error {
		return s.read(key, v, path, allowDisabled)
	})
	if err == nil && s.disabled && (s.keepLast != nil || s.keepWithinDays != nil) {
		err = fmt.Errorf("%w: %s: disabled cannot be combined with keep_last or keep_within_days", ErrInvalidRetentionFloor, path)
	}
	return s, err
}

// read takes one key of a floor section.
func (s *floorSettings) read(key string, v *yaml.Node, path string, allowDisabled bool) (err error) {
	switch key {
	case "keep_last":
		s.keepLast, err = readCount(v, path+"."+key)
	case "keep_within_days":
		s.keepWithinDays, err = readCount(v, path+"."+key)
	case "disabled":
		err = s.readDisabled(v, path, allowDisabled)
	default:
		err = keyError(path + "." + key)
	}
	return err
}

func (s *floorSettings) readDisabled(v *yaml.Node, path string, allowed bool) error {
	if !allowed {
		return fmt.Errorf("%w: %s.disabled: switch the floor off per repository in retention_floor.repositories.<name>", ErrInvalidRetentionFloor, path)
	}
	if err := v.Decode(&s.disabled); err != nil {
		return fmt.Errorf("%w: %s.disabled: want true or false", ErrInvalidRetentionFloor, path)
	}
	return nil
}

func keyError(key string) error {
	return fmt.Errorf("%w: %s: unknown key", ErrInvalidRetentionFloor, key)
}

// decodeMap calls visit for every key of a mapping; any other node is an
// error that names path.
func decodeMap(n *yaml.Node, path string, visit func(key string, value *yaml.Node) error) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s: want a mapping", ErrInvalidRetentionFloor, path)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if err := visit(n.Content[i].Value, n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// readCount reads an integer >= 0; a float or a string is an error.
func readCount(n *yaml.Node, path string) (*uint32, error) {
	v, err := strconv.ParseUint(n.Value, 10, 32)
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" || err != nil {
		return nil, fmt.Errorf("%w: %s: want a whole number >= 0, got %q", ErrInvalidRetentionFloor, path, n.Value)
	}
	count := uint32(v)
	return &count, nil
}

// builtInFloor is the floor of a host without a retention_floor section.
var builtInFloor = Floor{KeepLast: BuiltInFloorKeepLast, KeepWithinDays: BuiltInFloorKeepWithinDays, Origin: FloorBuiltIn}

// over applies the keys that are set to f.
func (s floorSettings) over(f Floor) Floor {
	if s.keepLast != nil {
		f.KeepLast = *s.keepLast
	}
	if s.keepWithinDays != nil {
		f.KeepWithinDays = *s.keepWithinDays
	}
	return f
}

// defaultFloor is the floor of a repository the section names nowhere.
func (f RetentionFloor) defaultFloor() Floor {
	if f.Default == nil {
		return builtInFloor
	}
	floor := f.Default.over(builtInFloor)
	floor.Origin = FloorConfigDefault
	return floor
}

// FloorOf is the floor of the repository: what retention_floor says for its
// name, else retention_floor.default, else the built-in one. A field a
// section leaves out comes from the section below it.
func (c Config) FloorOf(repository string) Floor {
	floor := c.RetentionFloor.defaultFloor()
	s, ok := c.RetentionFloor.Repositories[repository]
	if !ok {
		return floor
	}
	floor = s.over(floor)
	floor.Origin = FloorConfigRepository
	floor.Disabled = s.disabled
	if floor.Disabled {
		floor.KeepLast, floor.KeepWithinDays = 0, 0
	}
	return floor
}

// UnknownFloorRepositories are the names retention_floor.repositories holds
// that are no repository of the host, sorted.
func (c Config) UnknownFloorRepositories() []string {
	var unknown []string
	for name := range c.RetentionFloor.Repositories {
		if !slices.ContainsFunc(c.Repositories, func(r Repository) bool { return r.Name == name }) {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	return unknown
}

// validateRetentionFloor refuses a floor that keeps nothing without being
// switched off: it would be a floor in name only.
func (c Config) validateRetentionFloor() error {
	if c.RetentionFloor.Default != nil {
		if err := emptyFloor(c.RetentionFloor.defaultFloor(), "retention_floor.default"); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(mapKeysOf(c.RetentionFloor.Repositories)) {
		if err := emptyFloor(c.FloorOf(name), "retention_floor.repositories."+name); err != nil {
			return err
		}
	}
	return nil
}

func emptyFloor(f Floor, path string) error {
	if f.Disabled || f.KeepLast != 0 || f.KeepWithinDays != 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: keep_last 0 and keep_within_days 0 keep nothing; to switch the floor off say \"disabled: true\"", ErrInvalidRetentionFloor, path)
}

func mapKeysOf[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
