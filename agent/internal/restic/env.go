// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ErrInvalidEnvFile is returned for an env_file restic must not get.
// Its messages name variables and line numbers, never values.
var ErrInvalidEnvFile = errors.New("invalid env_file")

// baseEnv is the environment of every restic process.
func (c *CLI) baseEnv() []string {
	return []string{
		"PATH=" + c.opts.Path,
		"HOME=" + c.opts.CacheDir,
		"RESTIC_CACHE_DIR=" + c.opts.CacheDir,
	}
}

// repoEnv adds the repository, its key from the crypto.Provider and the
// backend credentials from env_file.
func (c *CLI) repoEnv(ctx context.Context) ([]string, error) {
	key, err := c.opts.Keys.RepositoryKey(ctx, c.repo.Name)
	if err != nil {
		return nil, err
	}
	backend, err := c.envFile()
	if err != nil {
		return nil, err
	}
	env := append(c.baseEnv(), "RESTIC_REPOSITORY="+c.repo.URL)
	env = append(env, key.Env...)
	return append(env, backend...), nil
}

// envFile reads the repository's env_file, if it has one.
func (c *CLI) envFile() ([]string, error) {
	if c.repo.EnvFile == "" {
		return nil, nil
	}
	data, err := c.opts.ReadFile(c.repo.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("env_file: %w", err)
	}
	env, err := parseEnv(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: env_file %q: %s", ErrInvalidEnvFile, c.repo.EnvFile, err.Error())
	}
	return env, nil
}

// parseEnv reads KEY=VALUE lines; blank lines and "#" comments are skipped.
// There is no quoting, "export" or expansion: the value is the rest of the line.
func parseEnv(data string) ([]string, error) {
	var env []string
	n := 0
	for line := range strings.Lines(data) {
		n++
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := checkEnvLine(line); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		env = append(env, line)
	}
	return env, nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Variables that would change which repository or key restic uses, what
// code it runs, or where it writes, rather than how it reaches the backend.
var (
	forbiddenPrefixes = []string{"RESTIC_", "LD_", "XDG_"}
	forbiddenNames    = []string{
		"PATH", "HOME", "TMPDIR", "DEBUG_LOG", "TERM", "SSH_AUTH_SOCK",
		"GODEBUG", "GOTRACEBACK", "GOMAXPROCS", "GOGC", "GOMEMLIMIT",
	}
)

func checkEnvLine(line string) error {
	name, _, ok := strings.Cut(line, "=")
	if !ok || !envName.MatchString(name) {
		return errors.New("want KEY=VALUE")
	}
	if slices.Contains(forbiddenNames, name) || slices.ContainsFunc(forbiddenPrefixes, func(p string) bool {
		return strings.HasPrefix(name, p)
	}) {
		return fmt.Errorf("%s is not allowed", name)
	}
	return nil
}
