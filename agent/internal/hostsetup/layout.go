// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"fmt"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// Layout names the files the commands create, all next to the main
// config (Р2): agent.d for the fragments, secrets for the values.
type Layout struct {
	// Config is the main config file.
	Config string
}

func (l Layout) dir() string { return filepath.Dir(l.Config) }

// FragmentDir is agent.d.
func (l Layout) FragmentDir() string { return filepath.Join(l.dir(), config.FragmentDir) }

// RepositoryFragment is the fragment "repo add" writes for a repository.
func (l Layout) RepositoryFragment(name string) string {
	return filepath.Join(l.FragmentDir(), "repo-"+name+".yaml")
}

// SecretFragment is the fragment "secret set" writes for a secret.
func (l Layout) SecretFragment(name string) string {
	return filepath.Join(l.FragmentDir(), "secret-"+name+".yaml")
}

// LockFile is the lock of changes to the config (Р10).
func (l Layout) LockFile() string { return filepath.Join(l.FragmentDir(), ".sard-config.lock") }

// SecretsDir holds the value files and the password files.
func (l Layout) SecretsDir() string { return filepath.Join(l.dir(), "secrets") }

// SecretFile is the value file of a secret.
func (l Layout) SecretFile(name string) string { return filepath.Join(l.SecretsDir(), name) }

// PasswordFile is the password file of a repository.
func (l Layout) PasswordFile(name string) string {
	return filepath.Join(l.SecretsDir(), "restic-"+name+".pass")
}

// EnvFile is the env file of an S3 repository (Р30).
func (l Layout) EnvFile(name string) string {
	return filepath.Join(l.SecretsDir(), "restic-"+name+".env")
}

const fragmentHeader = "# Written by sard-agent; change it with `sard-agent repo` and `sard-agent secret`, not by hand.\n"

type repositoryEntry struct {
	Name         string `yaml:"name"`
	URL          string `yaml:"url"`
	PasswordFile string `yaml:"password_file"`
	EnvFile      string `yaml:"env_file,omitempty"`
}

// RepositoryYAML is the content of a repository's fragment.
func RepositoryYAML(name, url, passwordFile string) []byte {
	return RepositoryYAMLWithEnv(name, url, passwordFile, "")
}

// RepositoryYAMLWithEnv is RepositoryYAML for a repository with an env_file.
func RepositoryYAMLWithEnv(name, url, passwordFile, envFile string) []byte {
	return marshal(map[string][]repositoryEntry{"repositories": {{Name: name, URL: url, PasswordFile: passwordFile, EnvFile: envFile}}})
}

// SecretYAML is the content of a secret's fragment.
func SecretYAML(name, path string) []byte {
	return marshal(map[string]map[string]string{"secrets": {name: path}})
}

// marshal cannot fail: v is a map or a slice of strings.
func marshal(v any) []byte {
	data, _ := yaml.Marshal(v)
	return append([]byte(fragmentHeader), data...)
}

// ReferencedBy names the config key that already refers to path, "" if
// none does (Р12). The key skip is not counted: it is the caller's own.
func ReferencedBy(cfg config.Config, path, skip string) string {
	for _, ref := range references(cfg) {
		if ref.path == path && ref.key != skip {
			return ref.key
		}
	}
	return ""
}

type reference struct{ key, path string }

func references(cfg config.Config) []reference {
	refs := []reference{{"tls.key_file", cfg.TLS.KeyFile}, {"tls.cert_file", cfg.TLS.CertFile}, {"tls.ca_file", cfg.TLS.CAFile}}
	for _, r := range cfg.Repositories {
		refs = append(refs,
			reference{RepositoryKey(r.Name, "password_file"), r.PasswordFile},
			reference{RepositoryKey(r.Name, "env_file"), r.EnvFile})
	}
	for _, name := range cfg.SecretNames() {
		refs = append(refs, reference{SecretKey(name), cfg.Secrets[name]})
	}
	for _, name := range cfg.ScriptNames() {
		refs = append(refs, reference{"scripts." + name, cfg.Scripts[name]})
	}
	return refs
}

// RepositoryKey is how a message names a file key of a repository.
func RepositoryKey(name, field string) string {
	return fmt.Sprintf("%s of repository %q", field, name)
}

// SecretKey is how a message names a secret's path.
func SecretKey(name string) string { return "secrets." + name }
