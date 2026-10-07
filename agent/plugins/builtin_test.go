// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package plugins_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
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

// written are the plugins whose methods are no longer stubs.
var written = []string{"files", "postgresql", "e2e-slow"}

func TestEveryPluginMethodIsNotImplementedYet(t *testing.T) {
	ctx := context.Background()
	for _, p := range plugins.Builtin("1.2.3") {
		if slices.Contains(written, p.Name()) {
			continue // written: plugins/files (A6b), plugins/postgresql (F1) and plugins/e2eslow (T3s) tests
		}
		t.Run(p.Name(), func(t *testing.T) {
			if err := p.Prepare(ctx, nil, nil); !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Prepare: %v", err)
			}
			if d, err := p.Dump(ctx, nil, nil); len(d.Paths) != 0 || !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Dump: %v, %v", d, err)
			}
			if err := p.Stream(ctx, nil, nil, sdk.Dump{}, io.Discard); !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Stream: %v", err)
			}
		})
	}
}

// Built-in plugins ship with the agent and share its version; none can
// verify a restored copy yet (roadmap: restore verification, stage 2).
func TestBuiltinPluginsShareTheAgentVersionAndDoNotVerifyYet(t *testing.T) {
	for _, p := range plugins.Builtin("1.2.3") {
		if p.Version() != "1.2.3" {
			t.Errorf("%s: Version() = %q", p.Name(), p.Version())
		}
		if _, ok := p.(sdk.Verifier); ok {
			t.Errorf("%s implements sdk.Verifier before it can verify", p.Name())
		}
	}
}

func TestBuiltinPluginsGetHandlersThatBackUpAndRestore(t *testing.T) {
	reg := plugins.Registry("1.2.3")
	h := plugins.Handlers(reg, pluginhost.NewSecrets(nil, nil), nil, t.TempDir())
	for _, name := range reg.Names() {
		handler, ok := h.Handler(name)
		if !ok || len(handler.Actions()) != 2 {
			t.Errorf("%s: handler %v, %v", name, handler, ok)
		}
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
	plugins.Handlers(reg, pluginhost.NewSecrets(nil, nil), nil, t.TempDir())
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
