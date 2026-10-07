// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// schemaDoc is the parts of config_schema the scenarios read.
type schemaDoc struct {
	Properties           map[string]property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties *bool               `json:"additionalProperties"`
}

type property struct {
	Type        string                `json:"type"`
	Items       *property             `json:"items"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Examples    []json.RawMessage     `json:"examples"`
	Format      string                `json:"format"`
	I18n        map[string]translated `json:"x-sard-i18n"`
}

type translated struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func readSchema(t *testing.T) schemaDoc {
	t.Helper()
	var doc schemaDoc
	if err := json.Unmarshal(postgresql.Plugin{}.ConfigSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

var fields = []string{"host", "port", "database", "user", "password_ref", "tls_mode", "tls_root_cert",
	"exclude_schemas", "exclude_tables", "pg_dump_path", "include_globals", "globals_role_passwords"}

// Scenario: Схема postgresql проходит ограничения Register.
func TestTheSchemaPassesTheLimitsOfRegister(t *testing.T) {
	raw := postgresql.Plugin{}.ConfigSchema()
	var one map[string]any
	if err := json.Unmarshal(raw, &one); err != nil || len(raw) > 65536 || bytes.Contains(raw, []byte(`\u0000`)) {
		t.Fatalf("schema: %v, %d bytes", err, len(raw))
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.RegisterFormat(&jsonschema.Format{Name: sdk.SecretFormat, Validate: func(any) error { return nil }})
	if err := c.AddResource("sard://pg.json", doc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Compile("sard://pg.json"); err != nil {
		t.Fatalf("not a draft 2020-12 JSON Schema: %v", err)
	}
	if schema, _ := one["$schema"].(string); schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q", schema)
	}
}

// Scenario: Поле password_ref помечено как имя секрета агента.
func TestPasswordRefIsMarkedAsTheNameOfASecret(t *testing.T) {
	doc := readSchema(t)
	if p := doc.Properties["password_ref"]; p.Type != "string" || p.Format != sdk.SecretFormat {
		t.Errorf("password_ref = %+v", p)
	}
	if !slices.Contains(doc.Required, "password_ref") {
		t.Errorf("required = %q", doc.Required)
	}
	for name, p := range doc.Properties {
		if name != "password_ref" && p.Format == sdk.SecretFormat {
			t.Errorf("%s has format %s", name, p.Format)
		}
	}
}

// Scenario: В схеме нет поля для значения пароля.
func TestTheSchemaHasNoFieldForThePassword(t *testing.T) {
	doc := readSchema(t)
	if doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Errorf("additionalProperties = %v", doc.AdditionalProperties)
	}
	for _, name := range []string{"password", "passwd", "dsn", "url", "conninfo"} {
		if _, ok := doc.Properties[name]; ok {
			t.Errorf("the schema has the field %s", name)
		}
	}
	if !slices.Equal(slices.Sorted(maps.Keys(doc.Properties)), slices.Sorted(slices.Values(fields))) {
		t.Errorf("fields = %q", slices.Sorted(maps.Keys(doc.Properties)))
	}
}

// Scenario: У каждого поля схемы есть заголовок, описание, пример и перевод.
func TestEveryFieldOfTheSchemaHasATitleADescriptionAnExampleAndATranslation(t *testing.T) {
	doc := readSchema(t)
	for _, field := range fields {
		p := doc.Properties[field]
		if p.Title == "" || p.Description == "" || len(p.Examples) == 0 {
			t.Errorf("%s: %+v", field, p)
		}
		if ru := p.I18n["ru"]; !inRussian(ru) {
			t.Errorf("%s: ru %+v", field, ru)
		}
	}
}

// Scenario: У каждого поля схемы есть пример (он проходит схему поля).
func TestTheExamplesOfEveryFieldPassTheSchema(t *testing.T) {
	doc := readSchema(t)
	compiled, err := pluginhost.CompileSchema("postgresql", postgresql.Plugin{}.ConfigSchema(), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		for _, example := range doc.Properties[field].Examples {
			if err := compiled.Validate([]byte(configWith(t, field, example))); err != nil {
				t.Errorf("%s: example %s: %v", field, example, err)
			}
		}
	}
}

func inRussian(tr translated) bool {
	return tr.Title != "" && tr.Description != "" && strings.ContainsAny(tr.Title+tr.Description, "абвгдежзиклмнопрстуфхцчшщыэюя")
}

// configWith is a config of the required fields and one field with a value.
func configWith(t *testing.T, field string, value json.RawMessage) string {
	t.Helper()
	cfg := k(map[string]any{"include_globals": nil, "tls_mode": nil})
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		t.Fatal(err)
	}
	cfg[field] = v
	if field == "tls_root_cert" {
		cfg["tls_mode"] = "verify-full"
	}
	return js(cfg)
}

// Scenario: Описание поля паролей ролей предупреждает о хэшах в снимке.
func TestTheDescriptionOfRolePasswordsWarnsAboutHashes(t *testing.T) {
	d := strings.ToLower(readSchema(t).Properties["globals_role_passwords"].Description)
	for _, part := range []string{"hash", "password", "snapshot", "superuser"} {
		if !strings.Contains(d, part) {
			t.Errorf("description %q does not mention %q", d, part)
		}
	}
}

// Scenario: Описание исключений ссылается на синтаксис шаблонов pg_dump.
func TestTheDescriptionOfExcludesLinksToThePgDumpPatterns(t *testing.T) {
	const link = "https://www.postgresql.org/docs/current/app-pgdump.html"
	for _, field := range []string{"exclude_schemas", "exclude_tables"} {
		if d := readSchema(t).Properties[field].Description; !strings.Contains(d, link) {
			t.Errorf("%s: description = %q", field, d)
		}
	}
}

// Scenario: Схема готова для формы консоли (web/src/schemaForm.ts): поле формы
// — строка, число, флажок, список, выбор из значений или имя секрета; иначе
// консоль покажет вместо формы редактор JSON и поля с выбором секрета не будет.
func TestEveryFieldOfTheSchemaFitsTheFormOfTheConsole(t *testing.T) {
	for name, p := range readSchema(t).Properties {
		switch {
		case p.Type == "string", p.Type == "integer", p.Type == "number", p.Type == "boolean":
		case p.Type == "array" && p.Items != nil && p.Items.Type == "string":
		default:
			t.Errorf("%s: type %q is not a field of the console's form", name, p.Type)
		}
	}
}
