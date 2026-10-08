// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultServiceUser is the user of the sard-agent service unless the main
// config says otherwise (service.user).
const DefaultServiceUser = "sard-agent"

// FragmentDir is the directory of config fragments, next to the main
// config (docs/adr/0049-agent-config-overlay.md).
const FragmentDir = "agent.d"

// fragmentSuffix marks a file of FragmentDir the agent reads; anything
// else there (editor and package-manager leftovers) is skipped.
const fragmentSuffix = ".yaml"

// Service is who runs the agent service on this host.
type Service struct {
	// User is the service user; empty means DefaultServiceUser. Only the
	// main config may set it.
	User string `yaml:"user"`
}

// ServiceUser is the user the service runs as: service.user of the main
// config, or DefaultServiceUser.
func (c Config) ServiceUser() string {
	if c.Service.User != "" {
		return c.Service.User
	}
	return DefaultServiceUser
}

// PeekServiceUser reads service.user of the config at path and nothing
// else; a file that cannot be read or parsed gives DefaultServiceUser. It
// lets a command find out who the service user is before it reads the
// whole config (A8a: the right to run comes before the config).
func PeekServiceUser(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultServiceUser
	}
	var peek struct {
		Service Service `yaml:"service"`
	}
	if yaml.Unmarshal(data, &peek) != nil {
		return DefaultServiceUser
	}
	return Config{Service: peek.Service}.ServiceUser()
}

// DuplicateNameError: one repository or secret name is defined in two
// places. First is the file read earlier, Second the later one (they are
// the same file for a name repeated inside it).
type DuplicateNameError struct {
	// Kind is "repository" or "secret".
	Kind          string
	Name          string
	First, Second string
}

func (e *DuplicateNameError) Error() string {
	return fmt.Sprintf("DUPLICATE_NAME: %s %q is defined in %s and in %s", e.Kind, e.Name, e.First, e.Second)
}

// FragmentError is a fragment that is not a fragment: it has a key other
// than repositories and secrets.
type FragmentError struct{ File, Key string }

func (e *FragmentError) Error() string {
	return fmt.Sprintf("%s: key %q is not allowed in a fragment of %s: only repositories and secrets are", e.File, e.Key, FragmentDir)
}

// fragment is what a file of agent.d may hold.
type fragment struct {
	Repositories []Repository      `yaml:"repositories"`
	Secrets      map[string]string `yaml:"secrets"`
}

// sources remembers where each definition came from.
type sources struct {
	path    string
	secrets map[string]string
}

// RepositorySource is the file that defines the repository: the main
// config or a fragment; "" for an unknown name.
func (c Config) RepositorySource(name string) string {
	i := slices.IndexFunc(c.Repositories, func(r Repository) bool { return r.Name == name })
	switch {
	case i < 0:
		return ""
	case c.Repositories[i].Fragment != "":
		return c.Repositories[i].Fragment
	}
	return c.src.path
}

// SecretSource is the file that defines the secret; "" for an unknown name.
func (c Config) SecretSource(name string) string {
	return c.src.secrets[name]
}

// Path is the main config file this Config was loaded from.
func (c Config) Path() string { return c.src.path }

// FragmentPaths lists the fragment files of the config at path, in the
// order they are read; a missing directory is no fragments.
func FragmentPaths(path string) ([]string, error) {
	dir := filepath.Join(filepath.Dir(path), FragmentDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries { // ReadDir sorts by file name
		if strings.HasSuffix(e.Name(), fragmentSuffix) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

// overlay adds the definitions of the fragments next to path to c. The
// main config's names are registered first, so a clash names the main file.
func overlay(c Config, path string) (Config, error) {
	files, err := FragmentPaths(path)
	if err != nil {
		return Config{}, err
	}
	c.src = sources{path: path, secrets: map[string]string{}}
	owners := namesOf(c, path)
	for _, file := range files {
		frag, err := readFragment(file)
		if err != nil {
			return Config{}, err
		}
		if c, err = merge(c, frag, file, owners); err != nil {
			return Config{}, err
		}
	}
	return c, c.validateRepositories()
}

// namesOf is who defined each name of the main config: "repository
// <name>" and "secret <name>" map to the file.
func namesOf(c Config, path string) map[string]string {
	owners := map[string]string{}
	for _, r := range c.Repositories {
		owners["repository "+r.Name] = path
	}
	for name := range c.Secrets {
		owners["secret "+name] = path
		c.src.secrets[name] = path
	}
	return owners
}

func merge(c Config, frag fragment, file string, owners map[string]string) (Config, error) {
	if err := c.mergeRepositories(frag, file, owners); err != nil {
		return Config{}, err
	}
	if err := c.mergeSecrets(frag, file, owners); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) mergeRepositories(frag fragment, file string, owners map[string]string) error {
	for _, r := range frag.Repositories {
		if err := claim(owners, "repository", r.Name, file); err != nil {
			return err
		}
		r.Fragment = file
		c.Repositories = append(c.Repositories, r)
	}
	return nil
}

func (c *Config) mergeSecrets(frag fragment, file string, owners map[string]string) error {
	for _, name := range sortedKeys(frag.Secrets) {
		if err := claim(owners, "secret", name, file); err != nil {
			return err
		}
		if c.Secrets == nil {
			c.Secrets = map[string]string{}
		}
		c.Secrets[name] = frag.Secrets[name]
		c.src.secrets[name] = file
	}
	return nil
}

// claim records file as the definition of the name, or reports who has it.
func claim(owners map[string]string, kind, name, file string) error {
	key := kind + " " + name
	if first, taken := owners[key]; taken {
		return &DuplicateNameError{Kind: kind, Name: name, First: first, Second: file}
	}
	owners[key] = file
	return nil
}

// readFragment reads one fragment; every error names the file.
func readFragment(file string) (fragment, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return fragment{}, fmt.Errorf("reading fragment %s: %w", file, err)
	}
	if err := checkFragmentKeys(file, data); err != nil {
		return fragment{}, err
	}
	var frag fragment
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&frag); err != nil && !errors.Is(err, io.EOF) {
		return fragment{}, fmt.Errorf("parsing fragment %s: %w", file, err)
	}
	return frag, nil
}

// checkFragmentKeys refuses a fragment with a key other than repositories
// and secrets; the first such key in name order is the one named.
func checkFragmentKeys(file string, data []byte) error {
	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("parsing fragment %s: %w", file, err)
	}
	for _, key := range slices.Sorted(mapKeys(keys)) {
		if key != "repositories" && key != "secrets" {
			return &FragmentError{File: file, Key: key}
		}
	}
	return nil
}

func mapKeys(m map[string]yaml.Node) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
