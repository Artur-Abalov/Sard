// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package config reads the agent's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

// Config is the agent configuration file.
type Config struct {
	Server Server `yaml:"server"`
	TLS    TLS    `yaml:"tls"`
	Restic Restic `yaml:"restic"`
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

// Restic configures the storage side.
type Restic struct {
	// PasswordFile is handed to restic by path (RESTIC_PASSWORD_FILE).
	PasswordFile string `yaml:"password_file"`
}

// ErrNoServerAddress is returned when server.address is missing.
var ErrNoServerAddress = errors.New("server.address is required")

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
	if c.Server.Address == "" {
		return Config{}, ErrNoServerAddress
	}
	return c, nil
}
