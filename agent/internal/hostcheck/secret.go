// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// SecretLine is secret.readable (Ч17): the file of a secret can be read as
// the service reads it and holds something. size is how many bytes were
// read, err why they could not be; the value itself is never passed in.
func SecretLine(name, path, hint string, size int, err error) Line {
	line := Line{Check: CheckSecretReadable, Subject: name, Status: OK, Message: fmt.Sprintf("%s can be read and is not empty", path)}
	problem := ""
	switch {
	case err != nil:
		problem = secretProblem(err)
	case size == 0:
		problem = "is empty"
	}
	if problem == "" {
		return line
	}
	line.Status = Error
	line.Message = fmt.Sprintf("%s %s", path, problem)
	line.Hint = hint
	return line
}

// secretProblem says what is wrong with a file that could not be read.
func secretProblem(err error) string {
	switch {
	case errors.Is(err, syscall.ELOOP):
		return "is a symbolic link; the service does not follow links"
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "cannot be read: permission denied"
	case strings.Contains(err.Error(), "not a regular file"):
		return "is not a regular file"
	}
	return "cannot be used: " + reasonOf(err)
}

// BindingLine is secret.bound (S11): the secret is bound to a plugin and its
// targets by a file the service can use. described is the binding in a line
// when it is in force; err why its file cannot be used; bound is false when
// there is none: the agent then gives the secret to no plugin.
func BindingLine(name, path, hint, described string, bound bool, err error) Line {
	switch {
	case err != nil:
		return Line{Check: CheckSecretBound, Subject: name, Status: Error, Message: fmt.Sprintf("%s cannot be used: %s", path, reasonOf(err)), Hint: hint}
	case !bound:
		return Line{Check: CheckSecretBound, Subject: name, Status: Error, Message: fmt.Sprintf("no binding (%s): the agent gives the secret to no plugin", path), Hint: hint}
	}
	return Line{Check: CheckSecretBound, Subject: name, Status: OK, Message: "bound to " + described}
}

// SecretSetHint is how the operator sets the secret again: secret set with
// its binding, which it requires (S11). A secret of the main config, named by
// mainConfig, is fixed in that file: commands never change it.
func SecretSetHint(name, mainConfig string) string {
	if mainConfig != "" {
		return fmt.Sprintf("secret %q is defined in %s, which commands never change: fix it there", name, mainConfig)
	}
	return "sudo sard-agent secret set " + name + " --plugin <plugin> --target <host:port/database>"
}

// BindingHint is how the operator binds the secret: secret set, or for a
// secret of the main config, which secret set refuses, the binding file at
// path written by hand.
func BindingHint(name, path, mainConfig string) string {
	if mainConfig != "" {
		return fmt.Sprintf("secret %q is defined in %s, which commands never change: write its binding into %s by hand "+
			"(docs/operator/09-troubleshooting.md#secretbound)", name, mainConfig, path)
	}
	return SecretSetHint(name, "")
}

// InMainConfig is the main config when it defines the secret, "" when a
// fragment of agent.d does.
func InMainConfig(cfg config.Config, name string) string {
	if source := cfg.SecretSource(name); source == cfg.Path() {
		return source
	}
	return ""
}
