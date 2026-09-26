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

	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func TestBuiltinRegistryHasTheFourSourcePlugins(t *testing.T) {
	if got := plugins.Registry().Names(); !slices.Equal(got, []string{"files", "mysql", "network", "postgresql"}) {
		t.Fatalf("Names() = %v", got)
	}
}

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
// an object with 2..4 fields: the UI renders forms from it.
func TestEveryConfigSchemaIsValidJSONSchema(t *testing.T) {
	for _, p := range plugins.Builtin() {
		t.Run(p.Name(), func(t *testing.T) {
			s := compileSchema(t, p)
			if s.Types == nil || !slices.Contains(s.Types.ToStrings(), "object") {
				t.Errorf("schema root must be an object, got %v", s.Types)
			}
			if n := len(s.Properties); n < 2 || n > 4 {
				t.Errorf("schema has %d fields, want 2..4", n)
			}
		})
	}
}

func TestEveryPluginMethodIsNotImplementedYet(t *testing.T) {
	ctx := context.Background()
	for _, p := range plugins.Builtin() {
		t.Run(p.Name(), func(t *testing.T) {
			if err := p.Prepare(ctx, nil); !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Prepare: %v", err)
			}
			if d, err := p.Dump(ctx, nil); len(d.Paths) != 0 || !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Dump: %v, %v", d, err)
			}
			if err := p.Stream(ctx, sdk.Dump{}, io.Discard); !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Stream: %v", err)
			}
			if err := p.Verify(ctx, nil, "/restore"); !errors.Is(err, sdk.ErrNotImplemented) {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}
