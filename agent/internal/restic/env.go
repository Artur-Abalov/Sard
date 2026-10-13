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

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// ErrInvalidEnvFile is returned for an env_file restic must not get.
// Its messages name variables and line numbers, never values.
var ErrInvalidEnvFile = errors.New("invalid env_file")

// ErrPasswordFile: the agent could not read the password file of the
// repository for restic that runs as root (Р78).
var ErrPasswordFile = errors.New("password_file")

// BackendNotSupportedError: restic must not run as root against a storage of
// this kind, because no allowlist of its env_file variables exists (Р72).
type BackendNotSupportedError struct{ Kind string }

func (e *BackendNotSupportedError) Error() string {
	return fmt.Sprintf("restic does not run as root against a repository of kind %s: no variables of its env_file are allowed", e.Kind)
}

// CheckRootBackend: restic may run as root against the repository, because
// the env_file variables of its kind of storage are allowlisted (Р72).
func CheckRootBackend(repo config.Repository) error {
	if _, ok := rootEnvAllowed[repo.Backend()]; !ok {
		return &BackendNotSupportedError{Kind: repo.Backend()}
	}
	return nil
}

// CheckRootInputs reads what restic would get when it runs as root, without
// starting it: the kind of storage, the variables of the env_file and the
// password file. It is what repo restore does before it lists anything, so
// that a refusal comes first (Р72, Р78); RestoreTo checks the same again.
func (c *CLI) CheckRootInputs() error {
	if err := CheckRootBackend(c.repo); err != nil {
		return err
	}
	if _, err := c.envFile(); err != nil {
		return err
	}
	if _, err := c.opts.ReadFile(c.repo.PasswordFile); err != nil {
		return fmt.Errorf("%w of repository %q: %w", ErrPasswordFile, c.repo.Name, err)
	}
	return nil
}

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
	if c.opts.AsRoot {
		if err := CheckRootBackend(c.repo); err != nil {
			return nil, err
		}
	}
	backend, err := c.envFile()
	if err != nil {
		return nil, err
	}
	env := append(c.baseEnv(), "RESTIC_REPOSITORY="+c.repo.URL)
	if c.opts.AsRoot { // the password goes through a pipe (passwordPipe)
		return append(env, backend...), nil
	}
	key, err := c.opts.Keys.RepositoryKey(ctx, c.repo.Name)
	if err != nil {
		return nil, err
	}
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
	check := checkEnvLine
	if c.opts.AsRoot {
		check = rootLineCheck(c.repo.Backend())
	}
	env, err := parseEnv(string(data), check)
	if err != nil {
		return nil, fmt.Errorf("%w: env_file %q: %s", ErrInvalidEnvFile, c.repo.EnvFile, err.Error())
	}
	return env, nil
}

// parseEnv reads KEY=VALUE lines; blank lines and "#" comments are skipped.
// There is no quoting, "export" or expansion: the value is the rest of the line.
func parseEnv(data string, check func(line string) error) ([]string, error) {
	var env []string
	n := 0
	for line := range strings.Lines(data) {
		n++
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := check(line); err != nil {
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

func envLineName(line string) (string, error) {
	name, _, ok := strings.Cut(line, "=")
	if !ok || !envName.MatchString(name) {
		return "", errors.New("want KEY=VALUE")
	}
	return name, nil
}

// rootEnvAllowed are the variables of an env_file that restic may get when
// it runs as root, by the kind of the storage (Р72). The env_file belongs to
// the service user, who is the intruder of R3 and R4 of ADR 0050: any other
// variable (RCLONE_PASSWORD_COMMAND, AWS_SHARED_CREDENTIALS_FILE, HTTPS_PROXY,
// SSL_CERT_FILE, ...) would let it run code as root or read files of root.
var rootEnvAllowed = map[string][]string{
	"s3":    {"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_DEFAULT_REGION"},
	"local": nil, "sftp": nil, "rest": nil,
}

// rootLineCheck is the check of a line of an env_file for the storage kind.
func rootLineCheck(kind string) func(string) error {
	return func(line string) error {
		name, err := envLineName(line)
		if err != nil {
			return err
		}
		if !slices.Contains(rootEnvAllowed[kind], name) {
			return fmt.Errorf("%s is not allowed for a repository of kind %s when restic runs as root", name, kind)
		}
		return nil
	}
}

func checkEnvLine(line string) error {
	name, err := envLineName(line)
	if err != nil {
		return err
	}
	if slices.Contains(forbiddenNames, name) || slices.ContainsFunc(forbiddenPrefixes, func(p string) bool {
		return strings.HasPrefix(name, p)
	}) {
		return fmt.Errorf("%s is not allowed", name)
	}
	return nil
}

// ParseEnvFile checks the contents of an env_file the way restic will get
// it and returns its assignments. An error names the line and the variable,
// never a value.
func ParseEnvFile(data []byte) ([]string, error) {
	env, err := parseEnv(string(data), checkEnvLine)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidEnvFile, err.Error())
	}
	return env, nil
}
