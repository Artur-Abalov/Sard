// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"text/tabwriter"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
)

// runSecretList is "sard-agent secret list": the names of the secrets, their
// bindings (S11) and the files that define them. It opens no value file (Р22).
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
	rows := secretRows(cfg, hostsetup.Layout{Config: opts.configPath}, deps.readFile)
	if opts.json {
		printSecretsJSON(stdout, rows)
		return exitOK
	}
	printSecrets(stdout, rows, opts.configPath)
	return exitOK
}

func printSecrets(stdout io.Writer, rows []secretJSONRow, configPath string) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(stdout, "no secrets are configured in %s\n", configPath)
		return
	}
	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tDEFINED_IN\tBOUND_TO")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.Name, r.DefinedIn, r.boundTo())
	}
	_ = w.Flush()
}

type secretJSONRow struct {
	Name      string `json:"name"`
	DefinedIn string `json:"defined_in"`
	// Binding is the plugin and the targets of the secret, nil without one;
	// BindingError says why its file cannot be used (S11).
	Binding      *bindingJSON `json:"binding"`
	BindingError string       `json:"binding_error,omitempty"`
	described    string
}

// bindingJSON is a binding as secret list --json prints it.
type bindingJSON struct {
	Plugin                string   `json:"plugin"`
	Targets               []string `json:"targets"`
	Auth                  []string `json:"auth,omitempty"`
	AllowTLS              string   `json:"allow_tls,omitempty"`
	CAFile                string   `json:"ca_file,omitempty"`
	AllowNoChannelBinding bool     `json:"allow_no_channel_binding,omitempty"`
}

// boundTo is the BOUND_TO column: the binding, why it cannot be used, or
// that there is none and the agent gives the secret to no plugin.
func (r secretJSONRow) boundTo() string {
	switch {
	case r.BindingError != "":
		return "unusable: " + r.BindingError
	case r.Binding == nil:
		return "nothing (no binding: given to no plugin)"
	}
	return r.described
}

// secretRows are the secrets of the config with their bindings, by name. No
// value file is opened.
func secretRows(cfg config.Config, l hostsetup.Layout, read func(string) ([]byte, error)) []secretJSONRow {
	rows := []secretJSONRow{}
	for _, name := range cfg.SecretNames() {
		row := secretJSONRow{Name: name, DefinedIn: cfg.SecretSource(name)}
		data, err := read(l.SecretBinding(name))
		var b secretbind.Binding
		if err == nil {
			b, err = secretbind.Parse(data)
		}
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			row.BindingError = withoutPathError(err)
		default:
			row.Binding, row.described = toBindingJSON(b), describeBinding(b)
		}
		rows = append(rows, row)
	}
	return rows
}

func toBindingJSON(b secretbind.Binding) *bindingJSON {
	out := &bindingJSON{Plugin: b.Plugin, Targets: []string{}, Auth: b.Auth, AllowTLS: b.AllowTLS, CAFile: b.CAFile, AllowNoChannelBinding: b.AllowNoChannelBinding}
	for _, t := range b.Targets {
		out.Targets = append(out.Targets, t.String())
	}
	return out
}

// withoutPathError is the error without the path of its file: the column
// says which secret.
func withoutPathError(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// printSecretsJSON is Р21 and S11: {"secrets":[{"name","defined_in","binding"}]}.
func printSecretsJSON(stdout io.Writer, rows []secretJSONRow) {
	printJSON(stdout, struct {
		Secrets []secretJSONRow `json:"secrets"`
	}{Secrets: rows})
}
