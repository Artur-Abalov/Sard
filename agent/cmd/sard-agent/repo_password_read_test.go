// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"slices"
	"testing"
)

// ADR 0008: the agent hands the password file to restic by path and never
// reads it itself.

// watchReads makes the host record every file the agent reads.
func (h *repoHost) watchReads() *[]string {
	var read []string
	h.deps.readFile = func(name string) ([]byte, error) {
		read = append(read, name)
		return os.ReadFile(name)
	}
	return &read
}

func (h *repoHost) assertPasswordFilesAreOnlyPassedByPath(read *[]string) {
	h.t.Helper()
	for _, name := range *read {
		if name == h.pass() || name == h.pass2() {
			h.t.Fatalf("the agent read the password file %s", name)
		}
	}
	h.restic.mu.Lock()
	defer h.restic.mu.Unlock()
	for _, c := range h.restic.calls {
		if c.sub != "version" && envValue(c.env, "RESTIC_PASSWORD_FILE") == "" {
			h.t.Fatalf("restic %s got no RESTIC_PASSWORD_FILE", c.sub)
		}
	}
}

func TestRepoCommandsNeverReadThePasswordFile(t *testing.T) {
	cases := map[string]func(h *repoHost) int{
		"init": func(h *repoHost) int { code, _, _ := h.initCmd(); return code },
		"init --generate-password, new file": func(h *repoHost) int {
			h.removePass()
			code, _, _ := h.initCmd("--generate-password")
			return code
		},
		"init --generate-password, existing file": func(h *repoHost) int {
			code, _, _ := h.initCmd("--generate-password")
			return code
		},
		"list": func(h *repoHost) int { code, _, _ := h.listCmd(); return code },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newRepoHost(t)
			read := h.watchReads()
			assertCode(t, run(h), exitOK)
			if h.restic.backendCalls() == 0 {
				t.Fatal("restic was never asked about a repository")
			}
			if !slices.Contains(*read, h.envFile()) {
				t.Fatalf("the spy saw no read of the env file: %q", *read)
			}
			h.assertPasswordFilesAreOnlyPassedByPath(read)
		})
	}
}
