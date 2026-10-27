// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// The identifiers of the checks of the config (Ч10).
const (
	CheckConfigLoad       = "config.load"
	CheckConfigNames      = "config.names"
	CheckConfigReferences = "config.references"
)

// ConfigLoadLine is config.load: the main file and agent.d are read and
// parsed; fragments is how many fragments were read, err why the load failed.
func ConfigLoadLine(path string, fragments int, err error) Line {
	if err != nil {
		return Line{Check: CheckConfigLoad, Status: Error, Message: err.Error(), Hint: "see " + HelpURL(CheckConfigLoad)}
	}
	return Line{Check: CheckConfigLoad, Status: OK, Message: fmt.Sprintf("%s and %d fragments of agent.d are read", path, fragments)}
}

// IsDuplicateName says whether the load failed because one name is defined
// in two places: that is config.names, not config.load.
func IsDuplicateName(err error) bool {
	var dup *config.DuplicateNameError
	return errors.As(err, &dup)
}

// ConfigNamesLine is config.names; dup is nil when no name is defined twice.
func ConfigNamesLine(dup *config.DuplicateNameError) Line {
	if dup == nil {
		return Line{Check: CheckConfigNames, Status: OK, Message: "no repository or secret is defined in two places"}
	}
	return Line{Check: CheckConfigNames, Status: Error, Message: dup.Error(),
		Hint: fmt.Sprintf("remove one of the two: sudo sard-agent %s remove %s", removeVerb(dup.Kind), dup.Name)}
}

// Ref is a file the config refers to and the key that does.
type Ref struct{ Key, Path, Fix string }

// References lists the files the config refers to, in a fixed order.
func References(cfg config.Config) []Ref {
	var refs []Ref
	for _, f := range []struct{ key, path string }{{"tls.ca_file", cfg.TLS.CAFile}, {"tls.cert_file", cfg.TLS.CertFile}, {"tls.key_file", cfg.TLS.KeyFile}} {
		refs = append(refs, Ref{f.key, f.path, enrollForce})
	}
	refs = append(refs, Ref{"restic.path", cfg.Restic.Path, "install restic there, or remove restic.path from the config"})
	for _, r := range cfg.Repositories {
		fix := fmt.Sprintf("sudo sard-agent repo add %s <address>", r.Name)
		refs = append(refs, Ref{"repositories." + r.Name + ".password_file", r.PasswordFile, fix}, Ref{"repositories." + r.Name + ".env_file", r.EnvFile, fix})
	}
	for _, name := range cfg.SecretNames() {
		refs = append(refs, Ref{"secrets." + name, cfg.Secrets[name], SecretSetHint(name)})
	}
	for _, name := range cfg.ScriptNames() {
		refs = append(refs, Ref{"scripts." + name, cfg.Scripts[name], "create the script, or remove scripts." + name + " from the config"})
	}
	return withPath(refs)
}

func withPath(refs []Ref) []Ref {
	var got []Ref
	for _, r := range refs {
		if r.Path != "" {
			got = append(got, r)
		}
	}
	return got
}

// ReferencesLine is config.references: every file the config refers to
// exists; look is stat, which returns why a file cannot be looked at.
func ReferencesLine(refs []Ref, look func(path string) error) Line {
	var texts, fixes []string
	for _, r := range refs {
		if err := look(r.Path); err != nil {
			texts = append(texts, fmt.Sprintf("%s: %s (%s)", r.Key, r.Path, reasonOf(err)))
			fixes = append(fixes, r.Fix)
		}
	}
	if len(texts) == 0 {
		return Line{Check: CheckConfigReferences, Status: OK, Message: fmt.Sprintf("the %d files the config refers to exist", len(refs))}
	}
	return Line{Check: CheckConfigReferences, Status: Error, Message: strings.Join(texts, "; "),
		Hint: strings.Join(append(fixes, "see "+HelpURL(CheckConfigReferences)), "; ")}
}

// reasonOf is the operating system's answer without the path in it.
func reasonOf(err error) string {
	var pe interface{ Unwrap() error }
	if errors.As(err, &pe) && pe.Unwrap() != nil {
		return pe.Unwrap().Error()
	}
	return err.Error()
}

// removeVerb is the command that removes a repository or a secret.
func removeVerb(kind string) string {
	if kind == "repository" {
		return "repo"
	}
	return kind
}
