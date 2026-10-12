// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// The decisions of the commands that change the config are made here, from
// the config and the layout alone: nothing is read or written.

// SecretPlan is what the config says about the secret a command is about
// to change.
type SecretPlan struct {
	// OwnFragment is true if the secret is defined in the fragment this
	// command writes.
	OwnFragment bool
}

// PlanSecret is the decision of Р12: a name defined elsewhere is refused,
// and so is a value file that another key of the config uses.
func PlanSecret(cfg config.Config, l Layout, name string) (SecretPlan, *refusal.Failure) {
	source := cfg.SecretSource(name)
	own := l.SecretFragment(name)
	if source != "" && source != own {
		return SecretPlan{}, refusal.Fail(refusal.DefinedInConfig, "secret %q is defined in %s; commands never change it, edit that file instead", name, source)
	}
	if key := ReferencedBy(cfg, l.SecretFile(name), SecretKey(name)); key != "" {
		return SecretPlan{}, refusal.Fail(refusal.PathInUse, "%s is used by %s and would be overwritten", l.SecretFile(name), key)
	}
	return SecretPlan{OwnFragment: source == own}, nil
}

// AddPlan is what "repo add" finds in the config.
type AddPlan int

const (
	// AddNew: the name is not connected yet.
	AddNew AddPlan = iota
	// AddUnchanged: the fragment of this command already connects this address.
	AddUnchanged
)

// PlanRepository is Р12 and Р11 for a repository: a name defined elsewhere
// is refused, the same name at another address is a conflict, the same
// address again changes nothing.
func PlanRepository(cfg config.Config, l Layout, name, url string) (AddPlan, *refusal.Failure) {
	source := cfg.RepositorySource(name)
	if f := planFiles(cfg, l, name, url); f != nil {
		return AddNew, f
	}
	switch {
	case source == "":
		return AddNew, nil
	case source != l.RepositoryFragment(name):
		return AddNew, refusal.Fail(refusal.DefinedInConfig, "repository %q is defined in %s; commands never change it, edit that file instead", name, source)
	}
	if current := RepositoryNamed(cfg, name); !sameAddress(current, url) {
		return AddNew, refusal.Fail(refusal.RepositoryConflict, "repository %q is already connected to %s; to connect it to another address run `sudo sard-agent repo remove %s` first", name, config.RedactURL(current.URL), name)
	}
	return AddUnchanged, nil
}

// planFiles refuses a password file, and for S3 an env file, that another
// key of the config uses.
func planFiles(cfg config.Config, l Layout, name, url string) *refusal.Failure {
	if key := ReferencedBy(cfg, l.PasswordFile(name), RepositoryKey(name, "password_file")); key != "" {
		return refusal.Fail(refusal.PathInUse, "%s is used by %s and would be taken over", l.PasswordFile(name), key)
	}
	if (config.Repository{URL: url}).Backend() != "s3" {
		return nil
	}
	if key := ReferencedBy(cfg, l.EnvFile(name), RepositoryKey(name, "env_file")); key != "" {
		return refusal.Fail(refusal.PathInUse, "%s is used by %s and would be overwritten", l.EnvFile(name), key)
	}
	return nil
}

// sameAddress: a local path is compared cleaned, a remote address as given.
func sameAddress(current config.Repository, url string) bool {
	if current.Backend() == "local" {
		return filepath.Clean(current.URL) == url
	}
	return current.URL == url
}

// RepositoryNamed is the repository of the config by name; empty if there
// is none.
func RepositoryNamed(cfg config.Config, name string) config.Repository {
	for _, r := range cfg.Repositories {
		if r.Name == name {
			return r
		}
	}
	return config.Repository{}
}

// Removal is what a remove command finds in the config.
type Removal int

const (
	// NothingToRemove: the name is not defined.
	NothingToRemove Removal = iota
	// RemoveOwn: the name is defined in the fragment the add command wrote.
	RemoveOwn
)

// PlanRemoval: a name defined in the main config or in a fragment this
// command did not write is refused (Р12).
func PlanRemoval(kind, name, source, own string) (Removal, *refusal.Failure) {
	switch {
	case source == "":
		return NothingToRemove, nil
	case source != own:
		return NothingToRemove, refusal.Fail(refusal.DefinedInConfig, "%s %q is defined in %s; commands never change it, edit that file instead", kind, name, source)
	}
	return RemoveOwn, nil
}
