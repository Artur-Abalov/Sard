// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Defaults of the directories the restore dir is judged against; they are
// the ones of the agent when the config sets none.
const (
	// DefaultRestoreDir lies inside the StateDirectory of the packaged unit,
	// so the service can write it without a drop-in.
	DefaultRestoreDir = "/var/lib/sard-agent/restore"
	// DefaultExecutorStateDir is executor.state_dir when the config sets none.
	DefaultExecutorStateDir = "/var/lib/sard-agent/executor"
	// DefaultResticCacheDir is restic.cache_dir when the config sets none.
	DefaultResticCacheDir = "/var/cache/sard/restic"
)

// Restore is the host's decision where the console may restore to. Only
// the operator of the host sets it: the protocol has no field for it.
type Restore struct {
	// Dir is the directory every restore from the console writes into, as
	// a new subdirectory; empty means DefaultRestoreDir.
	Dir string `yaml:"dir"`
	// PostgresqlLockShare is the share of the lock table of a PostgreSQL
	// cluster one pg_restore transaction may fill (ВК28); nil means
	// DefaultLockShare.
	PostgresqlLockShare *LockShare `yaml:"postgresql_lock_share"`
}

// DefaultLockShare is restore.postgresql_lock_share when the config sets none.
const DefaultLockShare = 0.5

// PostgresqlLockShare is restore.postgresql_lock_share, or DefaultLockShare.
func (c Config) PostgresqlLockShare() float64 {
	if c.Restore.PostgresqlLockShare == nil {
		return DefaultLockShare
	}
	return float64(*c.Restore.PostgresqlLockShare)
}

// ErrInvalidRestore is returned for a restore.dir the agent cannot use.
var ErrInvalidRestore = errors.New("invalid restore setting")

// RestoreDir is restore.dir, or DefaultRestoreDir.
func (c Config) RestoreDir() string {
	if c.Restore.Dir != "" {
		return c.Restore.Dir
	}
	return DefaultRestoreDir
}

// validateRestore: an absolute clean path that is not "/" and neither is
// nor lies in, nor holds, the state of the executor, the cache of restic or
// a local repository.
func (c Config) validateRestore() error {
	dir := c.RestoreDir()
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return fmt.Errorf("%w: restore.dir: want an absolute path without . or .. components and not /, got %q", ErrInvalidRestore, dir)
	}
	for _, other := range c.avoidedByRestore() {
		if overlaps(dir, other.path) {
			return fmt.Errorf("%w: restore.dir %s overlaps %s, %s; use a directory of its own", ErrInvalidRestore, dir, other.path, other.what)
		}
	}
	return nil
}

type avoided struct{ path, what string }

// avoidedByRestore are the directories restore.dir must keep apart from.
func (c Config) avoidedByRestore() []avoided {
	state, cache := c.Executor.StateDir, c.Restic.CacheDir
	if state == "" {
		state = DefaultExecutorStateDir
	}
	if cache == "" {
		cache = DefaultResticCacheDir
	}
	dirs := []avoided{{filepath.Clean(state), "executor.state_dir"}, {filepath.Clean(cache), "restic.cache_dir"}}
	for _, r := range c.Repositories {
		if path, local := r.LocalPath(); local {
			dirs = append(dirs, avoided{path, fmt.Sprintf("the local repository %q", r.Name)})
		}
	}
	return dirs
}

// LocalPath is the directory of a repository on this host's disk: its URL
// is a path or "local:<path>"; false for any other kind of storage.
func (r Repository) LocalPath() (string, bool) {
	if r.Backend() != "local" {
		return "", false
	}
	return filepath.Clean(strings.TrimPrefix(r.URL, "local:")), true
}

// overlaps: a and b are one directory, or one lies inside the other.
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/") || strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/")
}

// LockShare is a number from 0 to 1: restore.postgresql_lock_share.
type LockShare float64

// UnmarshalYAML refuses anything but a number from 0 to 1, naming the setting.
func (s *LockShare) UnmarshalYAML(n *yaml.Node) error {
	var v float64
	if err := n.Decode(&v); err != nil || math.IsNaN(v) || v < 0 || v > 1 {
		return fmt.Errorf("%w: restore.postgresql_lock_share: want a number from 0 to 1, got %q (line %d)", ErrInvalidRestore, n.Value, n.Line)
	}
	*s = LockShare(v)
	return nil
}
