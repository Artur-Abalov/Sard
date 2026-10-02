// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package stepsecrets says which values a step's logs and result must not
// contain (A7c). The rule stands until A7b replaces it: every secret of the
// agent, plus the values of the step repository's env_file and the
// password in its URL. The repository's password_file is never read
// (ADR 0008): restic gets it by path.
package stepsecrets

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Source implements executor.Secrets from the agent's config. Files are
// read for every step, so a rotated secret is picked up.
type Source struct {
	secrets map[string]string
	repos   map[string]config.Repository
	read    func(name string) ([]byte, error)
}

// New returns the Source of cfg; read reads a file.
func New(cfg config.Config, read func(name string) ([]byte, error)) *Source {
	repos := make(map[string]config.Repository, len(cfg.Repositories))
	for _, r := range cfg.Repositories {
		repos[r.Name] = r
	}
	return &Source{secrets: cfg.Secrets, repos: repos, read: read}
}

// For implements executor.Secrets. Errors name the secret or the
// repository, never a value or a path.
func (s *Source) For(step *agentv1.RunStep) ([]executor.Secret, error) {
	var out []executor.Secret
	for _, name := range slices.Sorted(maps.Keys(s.secrets)) {
		data, err := s.read(s.secrets[name])
		if err != nil {
			return nil, fmt.Errorf("cannot read secret %q: %w", name, withoutPath(err))
		}
		// A secret file usually ends with a line break the value lacks.
		out = append(out, executor.Secret{Name: "secret " + name, Value: bytes.TrimRight(data, "\r\n")})
	}
	// A step without a known repository gets the zero one: no env_file, no URL.
	repo := s.repos[step.GetRepositoryName()]
	env, err := s.envFile(repo)
	if err != nil {
		return nil, err
	}
	out = append(out, env...)
	if p := config.URLPassword(repo.URL); p != "" {
		out = append(out, executor.Secret{Name: fmt.Sprintf("url of repository %q", repo.Name), Value: []byte(p)})
	}
	return out, nil
}

// envFile returns the values of the repository's env_file, as restic gets them.
func (s *Source) envFile(repo config.Repository) ([]executor.Secret, error) {
	if repo.EnvFile == "" {
		return nil, nil
	}
	data, err := s.read(repo.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read env_file of repository %q: %w", repo.Name, withoutPath(err))
	}
	env, err := restic.ParseEnvFile(data)
	if err != nil {
		return nil, fmt.Errorf("env_file of repository %q: %w", repo.Name, err)
	}
	out := make([]executor.Secret, len(env))
	for i, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		out[i] = executor.Secret{Name: fmt.Sprintf("env_file of repository %q: %s", repo.Name, name), Value: []byte(value)}
	}
	return out, nil
}

// withoutPath drops the path from a file error: only names leave the host.
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
