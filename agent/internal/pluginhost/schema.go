// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// errUnknownSecret fails the sard-secret format.
var errUnknownSecret = errors.New("unknown secret")

// Schema is a compiled plugin ConfigSchema. It is safe for concurrent use.
type Schema struct {
	schema *jsonschema.Schema
}

// CompileSchema compiles a JSON Schema (draft 2020-12 unless it says
// otherwise) with formats asserted. A string of format "sard-secret" must
// be a name for which isSecret is true.
func CompileSchema(plugin string, schema []byte, isSecret func(name string) bool) (*Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("plugin %q: config schema: %w", plugin, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.RegisterFormat(&jsonschema.Format{Name: sdk.SecretFormat, Validate: secretName(isSecret)})
	url := "sard://plugins/" + plugin + "/config.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("plugin %q: config schema: %w", plugin, err)
	}
	compiled, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: config schema: %w", plugin, err)
	}
	return &Schema{schema: compiled}, nil
}

func secretName(isSecret func(string) bool) func(any) error {
	return func(v any) error {
		if name, ok := v.(string); ok && !isSecret(name) {
			return errUnknownSecret
		}
		return nil
	}
}

// Validate checks a config against the schema. Every violation is
// reported, with the JSON Pointer of its value, in a *sdk.ConfigError.
func (s *Schema) Validate(cfg []byte) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(cfg))
	if err != nil {
		return &sdk.ConfigError{Violations: []sdk.Violation{{Message: "not a JSON document: " + err.Error()}}}
	}
	var invalid *jsonschema.ValidationError
	if err := s.schema.Validate(doc); errors.As(err, &invalid) {
		return &sdk.ConfigError{Violations: violations(invalid)}
	} else if err != nil {
		return &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	return nil
}

// violations lists the leaves of the error tree, sorted by path.
func violations(root *jsonschema.ValidationError) []sdk.Violation {
	var out []sdk.Violation
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		for _, c := range e.Causes {
			walk(c)
		}
		if len(e.Causes) == 0 {
			out = append(out, violation(e))
		}
	}
	walk(root)
	slices.SortStableFunc(out, func(a, b sdk.Violation) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Message, b.Message))
	})
	return out
}

// violation describes a leaf error; its basic output carries the JSON
// Pointer of the value and the library's English message.
func violation(e *jsonschema.ValidationError) sdk.Violation {
	out := e.BasicOutput()
	if f, ok := e.ErrorKind.(*kind.Format); ok && f.Want == sdk.SecretFormat {
		name, _ := f.Got.(string)
		return sdk.Violation{Path: out.InstanceLocation, Message: fmt.Sprintf("%s %q", errUnknownSecret, name), Secret: name}
	}
	return sdk.Violation{Path: out.InstanceLocation, Message: out.Error.String()}
}
