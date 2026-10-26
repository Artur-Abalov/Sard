// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package plugins_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// compileSchema compiles a plugin's ConfigSchema as a draft 2020-12
// JSON Schema, failing the test when it is not one.
func compileSchema(t *testing.T, p sdk.Plugin) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(p.ConfigSchema()))
	if err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	url := "sard://plugins/" + p.Name() + ".json"
	if err := c.AddResource(url, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(url)
	if err != nil {
		t.Fatalf("invalid JSON Schema: %v", err)
	}
	return s
}

// Every plugin's ConfigSchema must be a valid draft 2020-12 JSON Schema of
// an object with at least 2 fields: the UI renders forms from it. (The upper
// bound of 4 fields of the stub era is gone: the postgresql plugin of F1 has 12.)
func TestEveryConfigSchemaIsValidJSONSchema(t *testing.T) {
	for _, p := range plugins.Builtin("1.2.3") {
		t.Run(p.Name(), func(t *testing.T) {
			s := compileSchema(t, p)
			if s.Types == nil || !slices.Contains(s.Types.ToStrings(), "object") {
				t.Errorf("schema root must be an object, got %v", s.Types)
			}
			if n := len(s.Properties); n < 2 {
				t.Errorf("schema has %d fields, want at least 2", n)
			}
		})
	}
}

// Built-in plugins ship with the agent and share its version; each takes the
// fingerprint of its source and restores a copy for a check (V2). Only files
// completes its fingerprint after restic: the files that changed meanwhile.
func TestBuiltinPluginsShareTheAgentVersionAndFingerprintTheirSource(t *testing.T) {
	completes := map[string]bool{"files": true, "postgresql": false}
	seen := 0
	for _, p := range plugins.Builtin("1.2.3") {
		if p.Version() != "1.2.3" {
			t.Errorf("%s: Version() = %q", p.Name(), p.Version())
		}
		if _, release := completes[p.Name()]; !release {
			continue // a plugin of the e2e stand (tag e2e) takes no fingerprint
		}
		seen++
		if _, ok := p.(sdk.Fingerprinter); !ok {
			t.Errorf("%s is no sdk.Fingerprinter", p.Name())
		}
		if _, ok := p.(sdk.CopyRestorer); !ok {
			t.Errorf("%s is no sdk.CopyRestorer", p.Name())
		}
		if _, ok := p.(sdk.FingerprintCompleter); ok != completes[p.Name()] {
			t.Errorf("%s: sdk.FingerprintCompleter %v", p.Name(), ok)
		}
	}
	if seen != len(completes) {
		t.Errorf("%d of the plugins of a release are built in", seen)
	}
}

// Every built-in plugin backs up, and both restore from the console (R2a, R2b).
func TestBuiltinPluginsGetHandlersThatBackUpAndRestore(t *testing.T) {
	reg := plugins.Registry("1.2.3")
	h := plugins.Handlers(reg, pluginhost.NewSecrets(nil, nil, nil), nil, t.TempDir())
	for _, name := range reg.Names() {
		handler, ok := h.Handler(name)
		wantActions := 1
		if name == "files" || name == "postgresql" {
			wantActions = 2
		}
		if !ok || len(handler.Actions()) != wantActions {
			t.Errorf("%s: handler %v, %v", name, handler, ok)
		}
	}
}

// R2c-2: the setting of the host that shapes the restore of PostgreSQL reaches
// the plugin through the options of the build; without one the plugin keeps its default.
func TestTheLockShareOfTheHostReachesThePostgresqlPlugin(t *testing.T) {
	share := func(opts ...plugins.Option) *float64 {
		for _, p := range plugins.Builtin("1.2.3", opts...) {
			if pg, ok := p.(postgresql.Plugin); ok {
				return pg.LockShare
			}
		}
		t.Fatal("no postgresql plugin")
		return nil
	}
	if got := share(); got != nil {
		t.Errorf("without an option the share is %v", *got)
	}
	for _, want := range []float64{0.25, 0} {
		if got := share(plugins.WithPostgresLockShare(want)); got == nil || *got != want {
			t.Errorf("share %v, want %v", got, want)
		}
	}
	if reg := plugins.Registry("1.2.3", plugins.WithPostgresLockShare(0.25)); len(reg.Names()) < 2 {
		t.Errorf("registry %v", reg.Names())
	}
}

type badSchema struct{ files.Plugin }

func (badSchema) ConfigSchema() []byte { return []byte(`{"type": 5}`) }

func TestHandlersPanicOnASchemaThatDoesNotCompile(t *testing.T) {
	reg, err := sdk.NewRegistry(badSchema{files.Plugin{AgentVersion: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	plugins.Handlers(reg, pluginhost.NewSecrets(nil, nil, nil), nil, t.TempDir())
}

// A version the server's format refuses is a programming error, not a
// registry without plugins.
func TestRegistryPanicsOnAnAgentVersionTheRegistryRefuses(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	plugins.Registry("a version with spaces")
}
