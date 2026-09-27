// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package config reads the agent's YAML configuration.
//
// Trust model (ADR 0008): repositories, secrets and scripts are defined
// here, on the agent host. The server refers to them by name only; their
// values never leave the host.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the agent configuration file.
type Config struct {
	Server       Server       `yaml:"server"`
	TLS          TLS          `yaml:"tls"`
	Repositories []Repository `yaml:"repositories"`
	// Secrets maps a secret name to the file holding its value.
	Secrets map[string]string `yaml:"secrets"`
	// Scripts maps a script name to the executable allowlisted for ACTION_RUN.
	Scripts  map[string]string `yaml:"scripts"`
	Restic   Restic            `yaml:"restic"`
	Executor Executor          `yaml:"executor"`
}

// Executor tunes how steps run. Both fields are optional.
type Executor struct {
	// StateDir keeps results until the server acknowledges them; empty means
	// /var/lib/sard-agent/executor. The executor creates it owner-only.
	StateDir string `yaml:"state_dir"`
	// MaxParallel is how many steps run at once; zero means one.
	MaxParallel int `yaml:"max_parallel"`
}

// Restic locates the restic binary shipped with the agent
// (docs/adr/0017-restic-shipped-with-agent.md). Both fields are optional.
type Restic struct {
	// Path is the restic executable; empty means "restic" next to sard-agent.
	Path string `yaml:"path"`
	// CacheDir is restic's cache; empty means /var/cache/sard/restic.
	CacheDir string `yaml:"cache_dir"`
}

// Server is where the agent dials in. The agent never listens.
type Server struct {
	// Address is host:port of the sard-server gRPC endpoint.
	Address string `yaml:"address"`
}

// TLS holds the mTLS material paths. Not used yet: see
// docs/adr/0009-agent-transport-mtls.md.
type TLS struct {
	CAFile   string `yaml:"ca_file"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Repository is a restic repository configured on this host.
type Repository struct {
	// Name is what the server uses in RunStep.repository_name.
	Name string `yaml:"name"`
	// URL is the restic repository string, e.g. "s3:https://host/bucket".
	URL string `yaml:"url"`
	// PasswordFile is handed to restic by path (RESTIC_PASSWORD_FILE).
	PasswordFile string `yaml:"password_file"`
	// EnvFile holds backend credentials, e.g. AWS_ACCESS_KEY_ID; optional.
	EnvFile string `yaml:"env_file"`
	// CryptoProvider selects the crypto.Provider; empty means restic AES.
	CryptoProvider string `yaml:"crypto_provider"`
}

// ErrNoServerAddress is returned when server.address is missing.
var ErrNoServerAddress = errors.New("server.address is required")

// ErrInvalidRepository is returned for an incomplete or duplicate repository.
var ErrInvalidRepository = errors.New("invalid repository")

// ErrInvalidRestic is returned for a relative restic.path or restic.cache_dir.
var ErrInvalidRestic = errors.New("want an absolute path")

// ErrInvalidExecutor is returned for a relative executor.state_dir or a negative executor.max_parallel.
var ErrInvalidExecutor = errors.New("invalid executor setting")

// Load reads and validates the file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse decodes YAML strictly (unknown keys are errors) and validates it.
func Parse(data []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if c.Server.Address == "" {
		return ErrNoServerAddress
	}
	if err := c.Restic.validate(); err != nil {
		return err
	}
	if err := c.Executor.validate(); err != nil {
		return err
	}
	var seen []string
	for i, r := range c.Repositories {
		if err := r.validate(seen); err != nil {
			return fmt.Errorf("repositories[%d]: %w", i, err)
		}
		seen = append(seen, r.Name)
	}
	return nil
}

func (r Repository) validate(seen []string) error {
	switch {
	case r.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidRepository)
	case slices.Contains(seen, r.Name):
		return fmt.Errorf("%w: duplicate name %q", ErrInvalidRepository, r.Name)
	case r.URL == "":
		return fmt.Errorf("%w: %q: url is required", ErrInvalidRepository, r.Name)
	case r.PasswordFile == "":
		return fmt.Errorf("%w: %q: password_file is required", ErrInvalidRepository, r.Name)
	}
	return nil
}

func (r Restic) validate() error {
	for _, f := range []struct{ key, value string }{{"restic.path", r.Path}, {"restic.cache_dir", r.CacheDir}} {
		if f.value != "" && !filepath.IsAbs(f.value) {
			return fmt.Errorf("%s: %w, got %q", f.key, ErrInvalidRestic, f.value)
		}
	}
	return nil
}

func (e Executor) validate() error {
	if e.StateDir != "" && !filepath.IsAbs(e.StateDir) {
		return fmt.Errorf("%w: executor.state_dir: want an absolute path, got %q", ErrInvalidExecutor, e.StateDir)
	}
	if e.MaxParallel < 0 {
		return fmt.Errorf("%w: executor.max_parallel: want zero (one step at a time) or more, got %d", ErrInvalidExecutor, e.MaxParallel)
	}
	return nil
}

// backends are the restic backend prefixes ("s3:...", "sftp:...").
var backends = []string{"azure", "b2", "gs", "rclone", "rest", "s3", "sftp", "swift"}

// Backend returns the restic backend kind of the repository URL; a plain
// path is "local".
func (r Repository) Backend() string {
	kind, _, found := strings.Cut(r.URL, ":")
	if found && slices.Contains(backends, kind) {
		return kind
	}
	return "local"
}

// SecretNames returns the configured secret names, sorted.
func (c Config) SecretNames() []string { return sortedKeys(c.Secrets) }

// ScriptNames returns the allowlisted script names, sorted.
func (c Config) ScriptNames() []string { return sortedKeys(c.Scripts) }

// PasswordFiles maps repository names to their restic password files.
func (c Config) PasswordFiles() map[string]string {
	files := make(map[string]string, len(c.Repositories))
	for _, r := range c.Repositories {
		files[r.Name] = r.PasswordFile
	}
	return files
}

func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
