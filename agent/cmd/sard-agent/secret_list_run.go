// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// runSecretList is "sard-agent secret list": the names of the secrets and
// the files that define them. It opens no value file (Р22).
func runSecretList(_ context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseSecretFlags("list", args, stderr, deps)
	if code != exitOK {
		return code
	}
	if _, f := authorize(deps, "secret list", opts, false, false); f != nil {
		return report(stderr, "secret list", f)
	}
	cfg, code := loadConfigFor("secret list", opts.configPath, stderr)
	if code != exitOK {
		return code
	}
	if opts.json {
		printSecretsJSON(stdout, cfg)
		return exitOK
	}
	printSecrets(stdout, cfg, opts.configPath)
	return exitOK
}

func printSecrets(stdout io.Writer, cfg config.Config, configPath string) {
	names := cfg.SecretNames()
	if len(names) == 0 {
		_, _ = fmt.Fprintf(stdout, "no secrets are configured in %s\n", configPath)
		return
	}
	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tDEFINED_IN")
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", name, cfg.SecretSource(name))
	}
	_ = w.Flush()
}

type secretJSONRow struct {
	Name      string `json:"name"`
	DefinedIn string `json:"defined_in"`
}

// printSecretsJSON is Р21: {"secrets":[{"name","defined_in"}]}.
func printSecretsJSON(stdout io.Writer, cfg config.Config) {
	out := struct {
		Secrets []secretJSONRow `json:"secrets"`
	}{Secrets: []secretJSONRow{}}
	for _, name := range cfg.SecretNames() {
		out.Secrets = append(out.Secrets, secretJSONRow{Name: name, DefinedIn: cfg.SecretSource(name)})
	}
	printJSON(stdout, out)
}
