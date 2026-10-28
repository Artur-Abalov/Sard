// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secretbind_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func mustBinding(t *testing.T, plugin string, targets ...string) secretbind.Binding {
	t.Helper()
	b, err := secretbind.New(plugin, targets)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestABindingSurvivesItsFile(t *testing.T) {
	b := mustBinding(t, "postgresql", "db.example.com:5432/app", "/var/run/postgresql:5432/*")
	b.Auth = []string{"scram-sha-256", "md5"}
	b.AllowTLS = "require"
	b.CAFile = "/etc/sard/pg-ca.pem"
	b.AllowNoChannelBinding = true
	data := b.Marshal()
	if !strings.HasPrefix(string(data), "# Written by sard-agent") {
		t.Errorf("the file does not say who wrote it:\n%s", data)
	}
	got, err := secretbind.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, b) {
		t.Errorf("Parse(Marshal(b)) = %+v, want %+v", got, b)
	}
	if g := got.Grant(); !reflect.DeepEqual(g, sdk.Grant{Auth: []string{"scram-sha-256", "md5"}, TLS: "require", CAFile: "/etc/sard/pg-ca.pem", NoChannelBinding: true}) {
		t.Errorf("Grant() = %+v", g)
	}
}

func TestAMinimalBindingIsPluginAndTargets(t *testing.T) {
	b := mustBinding(t, "postgresql", "db:5432/app")
	want := "plugin: postgresql\ntargets:\n    - db:5432/app\n"
	if !strings.HasSuffix(string(b.Marshal()), want) {
		t.Errorf("Marshal() =\n%s\nwant it to end with\n%s", b.Marshal(), want)
	}
	if g := b.Grant(); !reflect.DeepEqual(g, sdk.Grant{}) {
		t.Errorf("Grant() = %+v, want none", g)
	}
}

func TestABindingThatIsNotRightIsRefusedWithTheReason(t *testing.T) {
	cases := map[string]string{
		"targets: [db]\n":                                     "plugin",
		"plugin: postgresql\n":                                "target",
		"plugin: postgresql\ntargets: []\n":                   "target",
		"plugin: postgresql\ntargets: ['db:x']\n":             "port",
		"plugin: postgresql\ntargets: [db]\nauth: [kerb]\n":   "auth",
		"plugin: postgresql\ntargets: [db]\nauth: []\n":       "",
		"plugin: postgresql\ntargets: [db]\nallow_tls: x\n":   "allow_tls",
		"plugin: postgresql\ntargets: [db]\nca_file: rel\n":   "ca_file",
		"plugin: postgresql\ntargets: [db]\nunknown: 1\n":     "unknown",
		"plugin: Postgres SQL\ntargets: [db]\n":               "plugin",
		"plugin: postgresql\ntargets: [db]\n---\nplugin: x\n": "one document",
		"[": "yaml",
	}
	for doc, want := range cases {
		_, err := secretbind.Parse([]byte(doc))
		if want == "" {
			if err != nil {
				t.Errorf("Parse(%q): %v", doc, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error about %q", doc, err, want)
		}
	}
}

func TestNewRefusesWhatParseRefuses(t *testing.T) {
	if _, err := secretbind.New("postgresql", nil); err == nil {
		t.Error("a binding without targets was made")
	}
	if _, err := secretbind.New("postgresql", []string{"db:0"}); err == nil {
		t.Error("a binding with a wrong target was made")
	}
	if _, err := secretbind.New("", []string{"db"}); err == nil {
		t.Error("a binding without a plugin was made")
	}
}

var restored = sdk.Target{Host: "db.example.com", Port: 5432, Database: "restored"}

func TestASecretIsAllowedForItsPluginAtATargetOfItsBinding(t *testing.T) {
	b := mustBinding(t, "postgresql", "db.example.com:5432/app", "db.example.com:5432/*")
	if err := b.Allows("pg", "postgresql", restored); err != nil {
		t.Errorf("Allows(%s): %v", restored, err)
	}
}

func TestASecretIsNotAllowedAtAnotherTarget(t *testing.T) {
	b := mustBinding(t, "postgresql", "db.example.com:5432/app")
	evil := sdk.Target{Host: "evil.example", Port: 5432, Database: "app"}
	want := &sdk.BindingError{Name: "pg", Plugin: "postgresql", Target: evil}
	var be *sdk.BindingError
	if err := b.Allows("pg", "postgresql", evil); !errors.As(err, &be) || !reflect.DeepEqual(be, want) {
		t.Errorf("Allows(%s) = %#v, want %#v", evil, err, want)
	}
}

func TestASecretIsNotAllowedForAnotherPlugin(t *testing.T) {
	b := mustBinding(t, "postgresql", "db.example.com:5432/*")
	want := &sdk.BindingError{Name: "pg", Plugin: "files", Target: restored, BoundTo: "postgresql"}
	var be *sdk.BindingError
	if err := b.Allows("pg", "files", restored); !errors.As(err, &be) || !reflect.DeepEqual(be, want) {
		t.Errorf("Allows(files) = %#v, want %#v", err, want)
	}
}

func TestAnEmptyListOfMethodsIsNone(t *testing.T) {
	b, err := secretbind.Parse([]byte("plugin: postgresql\ntargets: [db]\nauth: []\n"))
	if err != nil || b.Auth != nil || !reflect.DeepEqual(b, mustBinding(t, "postgresql", "db")) {
		t.Errorf("Parse = %+v, %v; want no methods", b, err)
	}
}

// The server takes at most 64 targets and 64 methods of a binding
// (SnapshotRules.MAX_SECRET_TARGETS): a binding it would refuse is not made.
func TestABindingHasAtMostAsManyTargetsAndMethodsAsTheServerTakes(t *testing.T) {
	targets := func(n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("db%d", i))
		}
		return out
	}
	if _, err := secretbind.New("postgresql", targets(secretbind.MaxTargets)); err != nil {
		t.Errorf("%d targets: %v", secretbind.MaxTargets, err)
	}
	if _, err := secretbind.New("postgresql", targets(secretbind.MaxTargets+1)); err == nil || !strings.Contains(err.Error(), "at most 64 targets") {
		t.Errorf("%d targets: %v", secretbind.MaxTargets+1, err)
	}
	auth := func(n int) string {
		return "plugin: postgresql\ntargets: [db]\nauth: [" + strings.Repeat("md5,", n-1) + "md5]\n"
	}
	if _, err := secretbind.Parse([]byte(auth(secretbind.MaxTargets))); err != nil {
		t.Errorf("%d methods: %v", secretbind.MaxTargets, err)
	}
	if _, err := secretbind.Parse([]byte(auth(secretbind.MaxTargets + 1))); err == nil || !strings.Contains(err.Error(), "at most 64 methods") {
		t.Errorf("%d methods: %v", secretbind.MaxTargets+1, err)
	}
}
