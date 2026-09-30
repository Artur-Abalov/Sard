// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidConfig is matched by a *ConfigError.
var ErrInvalidConfig = errors.New("invalid plugin config")

// ErrUnknownSecret is matched by a *SecretError, and by a *ConfigError
// that names an unknown secret.
var ErrUnknownSecret = errors.New("unknown secret")

// ConfigError means a config does not satisfy the plugin's ConfigSchema,
// or a check of the plugin itself. The agent rejects the step before
// anything runs.
type ConfigError struct {
	Violations []Violation
}

// Violation is one reason a config is invalid.
type Violation struct {
	// Path is the JSON Pointer of the offending value, "" for the root.
	Path    string
	Message string
	// Secret is the unknown secret name the value holds, if that is the
	// violation.
	Secret string
}

func (e *ConfigError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		path := v.Path
		if path == "" {
			path = "(root)"
		}
		parts[i] = path + ": " + v.Message
	}
	return ErrInvalidConfig.Error() + ": " + strings.Join(parts, "; ")
}

// Is matches ErrInvalidConfig, and ErrUnknownSecret when a violation names
// an unknown secret.
func (e *ConfigError) Is(target error) bool {
	switch target {
	case ErrInvalidConfig:
		return true
	case ErrUnknownSecret:
		for _, v := range e.Violations {
			if v.Secret != "" {
				return true
			}
		}
	}
	return false
}

// SecretError is returned by Host.Secret for a name not defined on the
// agent host.
type SecretError struct {
	Name string
}

func (e *SecretError) Error() string { return fmt.Sprintf("%s %q", ErrUnknownSecret, e.Name) }

// Is matches ErrUnknownSecret.
func (e *SecretError) Is(target error) bool { return target == ErrUnknownSecret }
