// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"bytes"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// MaxSecretSize is the largest secret value a command accepts (Р7).
const MaxSecretSize = 65536

// Terminal reads a line from the operator's terminal without echoing it.
// The implementation turns the echo off and puts it back, whatever
// happens (terminal_linux.go).
type Terminal interface {
	ReadSecret(prompt string) ([]byte, error)
}

// LinePrompter is a terminal that can also ask for a line with the echo
// on (the confirmation of a host key, which the operator should see).
type LinePrompter interface {
	ReadLine(prompt string) ([]byte, error)
}

// SourceFlags are the names of the two flags of a command, for its messages:
// --stdin and --from-file, or --password-stdin and --password-from-file.
type SourceFlags struct{ Stdin, File string }

// SourceOptions say where the value comes from: at most one of Stdin and
// File; neither means the terminal, when there is one.
type SourceOptions struct {
	Stdin bool
	File  string
	Flags SourceFlags
	// Subject names what is asked for at the terminal ("secret db").
	Subject string
}

// Source is the operator's input: standard input, files, the terminal.
type Source struct {
	Stdin io.Reader
	// Terminal is nil unless standard input is a terminal.
	Terminal Terminal
	Open     func(path string) (io.ReadCloser, error)
}

// CheckSources refuses two sources at once (SECRET_SOURCE_CONFLICT); it
// reads nothing, so a command can run it with its other flag checks.
func CheckSources(o SourceOptions) *refusal.Failure {
	if o.Stdin && o.File != "" {
		return refusal.Fail(refusal.SecretSourceConflict, "give the value one way only: %s or %s, not both", o.Flags.Stdin, o.Flags.File)
	}
	return nil
}

// Read returns the value, byte for byte as given by --stdin and --from-file
// and as typed, without the line break, at the terminal (Н10). Without a
// flag and without a terminal nothing is read (SECRET_SOURCE_MISSING).
// Messages never contain the value.
func (s Source) Read(o SourceOptions) ([]byte, *refusal.Failure) {
	value, f := s.fetch(o)
	if f != nil {
		return nil, f
	}
	switch {
	case len(value) == 0:
		return nil, refusal.Fail(refusal.SecretEmpty, "the value is empty")
	case len(value) > MaxSecretSize:
		return nil, tooLarge()
	}
	return value, nil
}

func tooLarge() *refusal.Failure {
	return refusal.Fail(refusal.SecretTooLarge, "the value is larger than %d bytes", MaxSecretSize)
}

func (s Source) fetch(o SourceOptions) ([]byte, *refusal.Failure) {
	switch {
	case o.Stdin:
		return readLimited(s.Stdin, "standard input")
	case o.File != "":
		return s.readFile(o.File)
	case s.Terminal != nil:
		return askTwice(s.Terminal, o.Subject)
	}
	return nil, refusal.Fail(refusal.SecretSourceMissing, "there is no terminal to ask for the value and no source was given: use %s (standard input) or %s <path>", o.Flags.Stdin, o.Flags.File)
}

func (s Source) readFile(path string) ([]byte, *refusal.Failure) {
	f, err := s.Open(path)
	if err != nil {
		return nil, refusal.Fail(refusal.SecretSourceMissing, "cannot read %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	return readLimited(f, path)
}

// readLimited reads at most one byte more than the limit: enough to tell
// "too large" without reading all of a huge input.
func readLimited(r io.Reader, name string) ([]byte, *refusal.Failure) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSecretSize+1))
	if err != nil {
		return nil, refusal.Fail(refusal.SecretSourceMissing, "cannot read %s: %v", name, err)
	}
	return data, nil
}

// askTwice asks for the value twice; two different answers are refused.
func askTwice(t Terminal, subject string) ([]byte, *refusal.Failure) {
	first, err := t.ReadSecret(fmt.Sprintf("Value for %s: ", subject))
	if err != nil {
		return nil, terminalFailed(err)
	}
	second, err := t.ReadSecret("Repeat the value: ")
	if err != nil {
		return nil, terminalFailed(err)
	}
	if !bytes.Equal(first, second) {
		return nil, refusal.Fail(refusal.SecretMismatch, "the two values differ; nothing was changed")
	}
	return first, nil
}

func terminalFailed(err error) *refusal.Failure {
	return refusal.Fail(refusal.SecretSourceMissing, "cannot read the value from the terminal: %v", err)
}
