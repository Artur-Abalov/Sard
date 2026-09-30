// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

const dbSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "host": {"type": "string", "minLength": 1},
    "port": {"type": "integer", "minimum": 1, "maximum": 65535},
    "password": {"type": "string", "format": "sard-secret"},
    "replicas": {"type": "array", "items": {"type": "object", "properties": {"password": {"type": "string", "format": "sard-secret"}}}},
    "a/b~c": {"type": "string"}
  },
  "required": ["host"],
  "additionalProperties": false
}`

func compile(t *testing.T, schema string) *pluginhost.Schema {
	t.Helper()
	s, err := pluginhost.CompileSchema("db", []byte(schema), func(name string) bool { return name == "pg" })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func violations(t *testing.T, err error) []sdk.Violation {
	t.Helper()
	var cfgErr *sdk.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v, want a *sdk.ConfigError", err)
	}
	return cfgErr.Violations
}

func TestValidConfigIsAccepted(t *testing.T) {
	s := compile(t, dbSchema)
	if err := s.Validate([]byte(`{"host":"db1","port":5432,"password":"pg","replicas":[{"password":"pg"}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestViolationsCarryTheJSONPointerOfTheField(t *testing.T) {
	s := compile(t, dbSchema)
	got := violations(t, s.Validate([]byte(`{"host":"","port":70000,"a/b~c":1}`)))
	paths := make([]string, len(got))
	for i, v := range got {
		paths[i] = v.Path
		if v.Message == "" || v.Secret != "" {
			t.Errorf("violation %+v", v)
		}
	}
	if want := []string{"/a~1b~0c", "/host", "/port"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q (%+v)", paths, want, got)
	}
}

func TestMissingAndUnexpectedFieldsAreReportedAtTheirObject(t *testing.T) {
	s := compile(t, dbSchema)
	got := violations(t, s.Validate([]byte(`{"replicas":[{"password":"pg","x":1}],"extra":true}`)))
	if len(got) != 2 || got[0].Path != "" || got[1].Path != "" {
		t.Fatalf("violations = %+v", got)
	}
}

func TestUnknownSecretIsAViolationAtItsField(t *testing.T) {
	s := compile(t, dbSchema)
	err := s.Validate([]byte(`{"host":"db1","password":"mysql","replicas":[{"password":"pg"},{"password":"nope"}]}`))
	want := []sdk.Violation{
		{Path: "/password", Message: `unknown secret "mysql"`, Secret: "mysql"},
		{Path: "/replicas/1/password", Message: `unknown secret "nope"`, Secret: "nope"},
	}
	if got := violations(t, err); !slices.Equal(got, want) {
		t.Fatalf("violations = %+v\nwant %+v", got, want)
	}
	if !errors.Is(err, sdk.ErrUnknownSecret) || !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Errorf("errors.Is: %v", err)
	}
}

func TestSecretFormatIgnoresNonStrings(t *testing.T) {
	s := compile(t, `{"properties": {"password": {"format": "sard-secret"}}}`)
	if err := s.Validate([]byte(`{"password": 5}`)); err != nil {
		t.Fatal(err)
	}
}

func TestConfigThatIsNotOneJSONValueIsInvalid(t *testing.T) {
	s := compile(t, dbSchema)
	for _, cfg := range []string{``, `{`, `{"host":"a"} {}`} {
		got := violations(t, s.Validate([]byte(cfg)))
		if len(got) != 1 || got[0].Path != "" {
			t.Errorf("%q: violations = %+v", cfg, got)
		}
	}
}

func TestInvalidSchemaIsACompileError(t *testing.T) {
	for name, schema := range map[string]string{
		"not JSON":      `{`,
		"invalid type":  `{"type": "text"}`,
		"invalid regex": `{"pattern": "("}`,
	} {
		if _, err := pluginhost.CompileSchema("db", []byte(schema), nil); err == nil {
			t.Errorf("%s: compiled", name)
		}
	}
}
