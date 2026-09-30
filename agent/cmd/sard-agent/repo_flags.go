// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"flag"
	"fmt"
	"io"
	"time"
)

// repoOptions are the flags and the name of one "repo init" / "repo list" call.
type repoOptions struct {
	configPath string
	generate   bool
	timeout    time.Duration
	name       string
}

// parseRepoFlags reads the flags of "repo init" (a name, --generate-password)
// or "repo list" (no arguments). Flags may stand before and after the name
// (С1). No flag takes a password or a key (ADR 0008); a bad flag is never
// echoed with its value.
func parseRepoFlags(sub string, args []string, stderr io.Writer, deps repoDeps) (repoOptions, int) {
	fs := flag.NewFlagSet("sard-agent repo "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	configPath := fs.String("config", deps.defaultConfig, "")
	timeout := fs.Duration("timeout", defaultRepoTimeout, "")
	generate := generateFlag(fs, sub)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return repoOptions{}, repoUsage(stderr, sub, err.Error())
	}
	if msg := checkRepoArgs(sub, positional); msg != "" {
		return repoOptions{}, repoUsage(stderr, sub, msg)
	}
	if msg := checkRepoTimeout(*timeout); msg != "" {
		return repoOptions{}, repoUsage(stderr, sub, msg)
	}
	opts := repoOptions{configPath: *configPath, generate: *generate, timeout: *timeout}
	if len(positional) > 0 {
		opts.name = positional[0]
	}
	return opts, exitOK
}

// generateFlag registers --generate-password for repo init only.
func generateFlag(fs *flag.FlagSet, sub string) *bool {
	if sub == "init" {
		return fs.Bool("generate-password", false, "")
	}
	return new(bool)
}

// checkRepoTimeout is the message for a --timeout that is not positive.
func checkRepoTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return fmt.Sprintf("--timeout must be a positive duration, got %q", timeout.String())
	}
	return ""
}

// checkRepoArgs is the message for a wrong number of names: repo init
// takes one, repo list none.
func checkRepoArgs(sub string, positional []string) string {
	want := 0
	if sub == "init" {
		want = 1
	}
	switch {
	case len(positional) > want:
		return "unexpected extra argument"
	case len(positional) < want:
		return "the name of a repository of the agent config is required: sard-agent repo init [flags] <name>"
	}
	return ""
}

func repoUsage(stderr io.Writer, sub, msg string) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent repo %s: %s\n", sub, msg)
	return exitUsage
}

// parseInterspersed parses fs.Parse's flags before and after the
// positional arguments and returns the latter.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if args = fs.Args(); len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}
