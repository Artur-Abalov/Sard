// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func TestConfigErrorListsEveryViolationWithItsPath(t *testing.T) {
	err := &sdk.ConfigError{Violations: []sdk.Violation{
		{Path: "/paths", Message: "minItems: got 0, want 1"},
		{Path: "", Message: "missing property 'mode'"},
	}}
	want := "invalid plugin config: /paths: minItems: got 0, want 1; (root): missing property 'mode'"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
	if !errors.Is(fmt.Errorf("step: %w", err), sdk.ErrInvalidConfig) {
		t.Error("a ConfigError must match ErrInvalidConfig")
	}
	if errors.Is(err, sdk.ErrUnknownSecret) {
		t.Error("a ConfigError without unknown secrets must not match ErrUnknownSecret")
	}
}

func TestConfigErrorNamingAnUnknownSecretMatchesErrUnknownSecret(t *testing.T) {
	err := &sdk.ConfigError{Violations: []sdk.Violation{
		{Path: "/password", Message: `unknown secret "pg"`, Secret: "pg"},
	}}
	if !errors.Is(err, sdk.ErrUnknownSecret) || !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("errors.Is: %v", err)
	}
}

func TestSecretErrorNamesTheSecretOnly(t *testing.T) {
	err := &sdk.SecretError{Name: "pg"}
	if err.Error() != `unknown secret "pg"` {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(fmt.Errorf("prepare: %w", err), sdk.ErrUnknownSecret) {
		t.Error("a SecretError must match ErrUnknownSecret")
	}
	if errors.Is(err, sdk.ErrInvalidConfig) {
		t.Error("a SecretError is not a config error")
	}
}

func TestDumpIsStreamedWhenItHasAFilename(t *testing.T) {
	if (sdk.Dump{Paths: []string{"/srv"}}).Streamed() {
		t.Error("a dump by paths is not streamed")
	}
	if !(sdk.Dump{Filename: "db.sql"}).Streamed() {
		t.Error("a dump with a file name is streamed")
	}
}

// A plugin reports a program it cannot start with ErrProgramMissing, wrapped
// with %w; the agent recognises it with errors.Is, not by the words.
func TestErrProgramMissingIsFoundThroughWrapping(t *testing.T) {
	err := fmt.Errorf("prepare: mydb: %w: %q", sdk.ErrProgramMissing, "mongodump")
	if !errors.Is(err, sdk.ErrProgramMissing) {
		t.Error("a wrapped ErrProgramMissing must be found")
	}
	if errors.Is(err, sdk.ErrInvalidConfig) || errors.Is(err, sdk.ErrUnknownSecret) {
		t.Error("a missing program is neither a config error nor an unknown secret")
	}
	if errors.Is(errors.New(sdk.ErrProgramMissing.Error()), sdk.ErrProgramMissing) {
		t.Error("the same words are not the sentinel")
	}
}
