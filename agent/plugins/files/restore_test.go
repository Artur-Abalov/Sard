// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Restorer = files.Plugin{}

func restoreSchema(t *testing.T) *pluginhost.Schema {
	t.Helper()
	s, err := pluginhost.CompileSchema("files", files.Plugin{}.RestoreConfigSchema(), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// Scenario: Недопустимое назначение отклоняется по схеме.
func TestTheRestoreSchemaRejectsAnInvalidDestination(t *testing.T) {
	schema := restoreSchema(t)
	for name, dest := range map[string]string{
		"empty":                "",
		"parent":               "../etc",
		"parent in the middle": "s1/../../etc",
		"dot":                  "s1/./x",
		"double slash":         "s1//x",
		"staging":              ".sard-staging/c1",
		"hidden":               "s1/.hidden",
		"NUL":                  "s1\x00x",
		"absolute":             "/etc",
		"4097 characters":      strings.Repeat("a/", 2047) + "aaa",
		"component of 256":     strings.Repeat("a", 256),
	} {
		err := schema.Validate([]byte(`{"destination": ` + quote(dest) + `}`))
		var cfgErr *sdk.ConfigError
		if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "/destination") {
			t.Errorf("%s: Validate = %v, want a violation at /destination", name, err)
		}
	}
}

func TestTheRestoreSchemaAcceptsADestinationAndPaths(t *testing.T) {
	schema := restoreSchema(t)
	for _, cfg := range []string{
		`{"destination": "etc%20data/20261010T030000Z"}`,
		`{"destination": "s1/x", "paths": ["/srv/data", "/etc/hosts"]}`,
		`{"destination": "s1/x", "paths": []}`,
	} {
		if err := schema.Validate([]byte(cfg)); err != nil {
			t.Errorf("%s: %v", cfg, err)
		}
	}
}

// Scenario: Недопустимый путь части снимка отклоняется по схеме.
func TestTheRestoreSchemaRejectsInvalidPathsAndUnknownFields(t *testing.T) {
	schema := restoreSchema(t)
	hundredOne := make([]string, 101)
	for i := range hundredOne {
		hundredOne[i] = "/p" + strings.Repeat("x", i)
	}
	many, _ := json.Marshal(hundredOne)
	for name, cfg := range map[string]string{
		"relative path":     `{"destination": "x", "paths": ["srv/data"]}`,
		"star":              `{"destination": "x", "paths": ["/srv/*"]}`,
		"parent":            `{"destination": "x", "paths": ["/srv/../etc"]}`,
		"parent at the end": `{"destination": "x", "paths": ["/srv/.."]}`,
		"repeat":            `{"destination": "x", "paths": ["/srv/data", "/srv/data"]}`,
		"101 paths":         `{"destination": "x", "paths": ` + string(many) + `}`,
		"no destination":    `{}`,
		"overwrite":         `{"destination": "x", "overwrite": true}`,
		"a password":        `{"destination": "x", "password": "p"}`,
	} {
		if err := schema.Validate([]byte(cfg)); err == nil {
			t.Errorf("%s: %s passed the schema", name, cfg)
		}
	}
}

func TestEveryFieldOfTheRestoreSchemaHasATitleADescriptionAndATranslation(t *testing.T) {
	var doc schemaDoc
	if err := json.Unmarshal(files.Plugin{}.RestoreConfigSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"destination", "paths"} {
		p := doc.Properties[field]
		ru := p.I18n["ru"]
		if p.Title == "" || p.Description == "" || len(p.Examples) == 0 || ru.Title == "" || ru.Description == "" {
			t.Errorf("%s: %+v", field, p)
		}
	}
}

func TestThePlanOfARestoreIsTheDestinationAndThePaths(t *testing.T) {
	plan, err := files.Plugin{}.PlanRestore([]byte(`{"destination": "s1/x", "paths": ["/srv/data"]}`))
	if err != nil || plan.Destination != "s1/x" || len(plan.Paths) != 1 || plan.Paths[0] != "/srv/data" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func TestAPlanOfARestoreWithAnInvalidDestinationIsAConfigError(t *testing.T) {
	_, err := files.Plugin{}.PlanRestore([]byte(`{"destination": "` + strings.Repeat("ж", 128) + `"}`))
	if !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (files.Plugin{}).PlanRestore([]byte(`not json`)); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}
