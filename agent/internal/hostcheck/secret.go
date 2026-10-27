// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
)

// SecretLine is secret.readable (Ч17): the file of a secret can be read as
// the service reads it and holds something. size is how many bytes were
// read, err why they could not be; the value itself is never passed in.
func SecretLine(name, path string, size int, err error) Line {
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
	line.Hint = SecretSetHint(name)
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
func BindingLine(name, path, described string, bound bool, err error) Line {
	hint := SecretSetHint(name)
	switch {
	case err != nil:
		return Line{Check: CheckSecretBound, Subject: name, Status: Error, Message: fmt.Sprintf("%s cannot be used: %s", path, reasonOf(err)), Hint: hint}
	case !bound:
		return Line{Check: CheckSecretBound, Subject: name, Status: Error, Message: fmt.Sprintf("no binding (%s): the agent gives the secret to no plugin", path), Hint: hint}
	}
	return Line{Check: CheckSecretBound, Subject: name, Status: OK, Message: "bound to " + described}
}

// SecretSetHint is the command that sets the secret again: with its binding,
// which secret set requires (S11).
func SecretSetHint(name string) string {
	return "sudo sard-agent secret set " + name + " --plugin <plugin> --target <host:port/database>"
}
