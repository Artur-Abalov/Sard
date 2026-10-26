// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secretbind

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// header opens a file the command writes.
const header = "# Written by sard-agent secret set: the plugin and the targets this secret may be sent to.\n"

// pluginName is the format of the name of a plugin (sdk.Registry).
var pluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// authMethods are the methods of authentication a binding may allow, as libpq
// spells them in require_auth.
var authMethods = []string{"scram-sha-256", "md5", "password", "none"}

// tlsModes are the weakest TLS modes a binding may allow.
var tlsModes = []string{"require", "disable"}

// Binding says to which plugin and to which targets the value of a secret
// may go, and what the operator allows beyond the plugin's own rules.
type Binding struct {
	Plugin  string
	Targets []Pattern
	// Auth are the authentication methods allowed; empty: the plugin's default.
	Auth []string
	// AllowTLS is the weakest TLS mode allowed; empty: the plugin's default.
	AllowTLS string
	// CAFile is the CA the server's certificate is checked with.
	CAFile string
	// AllowNoChannelBinding allows TLS without channel binding.
	AllowNoChannelBinding bool
}

// document is the file of a binding.
type document struct {
	Plugin                string   `yaml:"plugin"`
	Targets               []string `yaml:"targets"`
	Auth                  []string `yaml:"auth,omitempty"`
	AllowTLS              string   `yaml:"allow_tls,omitempty"`
	CAFile                string   `yaml:"ca_file,omitempty"`
	AllowNoChannelBinding bool     `yaml:"allow_no_channel_binding,omitempty"`
}

// New is a binding of plugin to targets, nothing allowed beyond the plugin's
// rules.
func New(plugin string, targets []string) (Binding, error) {
	return fromDocument(document{Plugin: plugin, Targets: targets})
}

// Parse reads the file of a binding: one YAML document, no unknown key.
func Parse(data []byte) (Binding, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var d document
	if err := dec.Decode(&d); err != nil {
		return Binding{}, fmt.Errorf("yaml: %w", err)
	}
	var extra document
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Binding{}, errors.New("a binding is one document; the file holds more")
	}
	return fromDocument(d)
}

// fromDocument checks every field of d.
func fromDocument(d document) (Binding, error) {
	b := Binding{Plugin: d.Plugin, Auth: d.Auth, AllowTLS: d.AllowTLS, CAFile: d.CAFile, AllowNoChannelBinding: d.AllowNoChannelBinding}
	if !pluginName.MatchString(d.Plugin) {
		return Binding{}, fmt.Errorf("plugin %q is not the name of a plugin", d.Plugin)
	}
	if len(d.Targets) == 0 {
		return Binding{}, errors.New("a binding needs at least one target")
	}
	for _, s := range d.Targets {
		p, err := ParsePattern(s)
		if err != nil {
			return Binding{}, err
		}
		b.Targets = append(b.Targets, p)
	}
	if len(b.Auth) == 0 {
		b.Auth = nil
	}
	return b, b.Check()
}

// Check checks what the binding allows beyond the plugin's rules.
func (b Binding) Check() error {
	if err := checkAuth(b.Auth); err != nil {
		return err
	}
	if b.AllowTLS != "" && !slices.Contains(tlsModes, b.AllowTLS) {
		return fmt.Errorf("allow_tls %q is not one of %v", b.AllowTLS, tlsModes)
	}
	if b.CAFile != "" && !filepath.IsAbs(b.CAFile) {
		return fmt.Errorf("ca_file %q is not an absolute path", b.CAFile)
	}
	return nil
}

// checkAuth: every method is one libpq knows.
func checkAuth(methods []string) error {
	for _, m := range methods {
		if !slices.Contains(authMethods, m) {
			return fmt.Errorf("auth %q is not one of %v", m, authMethods)
		}
	}
	return nil
}

// Marshal is the content of the file of the binding.
func (b Binding) Marshal() []byte {
	d := document{Plugin: b.Plugin, Auth: b.Auth, AllowTLS: b.AllowTLS, CAFile: b.CAFile, AllowNoChannelBinding: b.AllowNoChannelBinding}
	for _, p := range b.Targets {
		d.Targets = append(d.Targets, p.String())
	}
	data, _ := yaml.Marshal(d) // cannot fail: strings, a list of strings, a bool
	return append([]byte(header), data...)
}

// Grant is what the binding allows the plugin.
func (b Binding) Grant() sdk.Grant {
	return sdk.Grant{Auth: slices.Clone(b.Auth), TLS: b.AllowTLS, CAFile: b.CAFile, NoChannelBinding: b.AllowNoChannelBinding}
}

// Allows is nil when the secret called name may go from plugin to target, a
// *sdk.BindingError otherwise.
func (b Binding) Allows(name, plugin string, target sdk.Target) error {
	if b.Plugin != plugin {
		return &sdk.BindingError{Name: name, Plugin: plugin, Target: target, BoundTo: b.Plugin}
	}
	for _, p := range b.Targets {
		if p.Match(target) {
			return nil
		}
	}
	return &sdk.BindingError{Name: name, Plugin: plugin, Target: target}
}
