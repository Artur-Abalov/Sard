// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package stepsecrets says which values a step's logs and result must not
// contain (A7b, ADR 0033): every secret of the agent, the values of the
// env_file of every repository, the agent's TLS private key and the password
// in the step repository's URL. A repository's password_file is never read
// (ADR 0008): restic gets it by path. It also warns about values so short
// that masking them makes logs less readable.
package stepsecrets

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/redact"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Source implements executor.Secrets from the agent's config. Files are
// read for every step, so a rotated secret is picked up.
type Source struct {
	secrets map[string]string
	keyFile string
	repos   []config.Repository
	read    func(name string) ([]byte, error)
	log     *slog.Logger

	mu    sync.Mutex
	short map[string]bool // values that were short when last read
}

// New returns the Source of cfg; read reads a file, log gets the warnings.
func New(cfg config.Config, read func(name string) ([]byte, error), log *slog.Logger) *Source {
	return &Source{secrets: cfg.Secrets, keyFile: cfg.TLS.KeyFile, repos: cfg.Repositories, read: read, log: log, short: map[string]bool{}}
}

// For implements executor.Secrets. Errors name the secret or the
// repository, never a value or a path.
func (s *Source) For(step *agentv1.RunStep) ([]executor.Secret, error) {
	out, errs := s.gather()
	if len(errs) > 0 {
		return nil, errs[0]
	}
	// A step without a known repository gets the zero one: no URL password.
	for _, repo := range s.repos {
		if repo.Name != step.GetRepositoryName() {
			continue
		}
		if p := config.URLPassword(repo.URL); p != "" {
			out = append(out, executor.Secret{Name: fmt.Sprintf("url of repository %q", repo.Name), Value: []byte(p)})
		}
	}
	s.warnShort(out)
	return out, nil
}

// Audit is the check at agent start: it reads every file the values come
// from and warns about the short values and about the files it cannot read.
// It stops nothing: a step that needs an unreadable file fails later.
func (s *Source) Audit() {
	out, errs := s.gather()
	for _, err := range errs {
		s.log.Warn("cannot read a file of masked values; steps will fail until it is readable", "error", err)
	}
	s.warnShort(out)
}

// gather reads every value of the agent; it goes on after an error.
func (s *Source) gather() ([]executor.Secret, []error) {
	var out []executor.Secret
	var errs []error
	add := func(secrets []executor.Secret, err error) {
		out = append(out, secrets...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(s.secrets)) {
		secrets, err := s.file("secret "+name, fmt.Sprintf("secret %q", name), s.secrets[name])
		if len(secrets) > 0 {
			secrets[0].Ref = name
		}
		add(secrets, err)
	}
	if s.keyFile != "" {
		add(s.file("tls.key_file", "tls.key_file", s.keyFile))
	}
	for _, repo := range s.repos {
		add(s.envFile(repo))
	}
	return out, errs
}

// file reads one value; a secret file usually ends with a line break the
// value lacks.
func (s *Source) file(name, what, path string) ([]executor.Secret, error) {
	data, err := s.read(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", what, withoutPath(err))
	}
	return []executor.Secret{{Name: name, Value: bytes.TrimRight(data, "\r\n"), Content: data}}, nil
}

// warnShort warns once about each value that turned short since it was last
// seen long (or never seen); an empty value is skipped by the masker.
func (s *Source) warnShort(secrets []executor.Secret) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range secrets {
		short := len(v.Value) > 0 && redact.IsShort(v.Value)
		if short && !s.short[v.Name] {
			s.log.Warn("a short secret masks the same text anywhere in step logs, which makes the logs less readable", "secret", v.Name)
		}
		s.short[v.Name] = short
	}
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
