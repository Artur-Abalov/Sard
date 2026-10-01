// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// schemaDoc is the parts of config_schema the scenarios read.
type schemaDoc struct {
	Properties map[string]property `json:"properties"`
}

type property struct {
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
	if err := json.Unmarshal(files.Plugin{}.ConfigSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

var fields = []string{"paths", "exclude", "one_file_system"}

// Scenario: У каждого поля схемы есть заголовок, описание и пример.
func TestEveryFieldOfTheSchemaHasATitleADescriptionAndExamples(t *testing.T) {
	doc := readSchema(t)
	for _, field := range fields {
		p := doc.Properties[field]
		if p.Title == "" || p.Description == "" || len(p.Examples) == 0 {
			t.Errorf("%s: %+v", field, p)
		}
	}
}

func TestTheExamplesOfEveryFieldPassTheSchema(t *testing.T) {
	compiled, err := pluginhost.CompileSchema("files", files.Plugin{}.ConfigSchema(), func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		for _, example := range readSchema(t).Properties[field].Examples {
			// The example of one field is a config of its own, with the
			// required paths beside it.
			cfg := `{"paths": ["/etc"], "` + field + `": ` + string(example) + `}`
			if err := compiled.Validate([]byte(cfg)); err != nil {
				t.Errorf("%s: example %s: %v", field, example, err)
			}
		}
	}
}

// Scenario: Описание exclude ссылается на синтаксис шаблонов restic.
func TestTheDescriptionOfExcludeLinksToTheResticDocumentation(t *testing.T) {
	const link = "https://restic.readthedocs.io/en/stable/040_backup.html#excluding-files"
	if d := readSchema(t).Properties["exclude"].Description; !strings.Contains(d, link) {
		t.Errorf("description = %q", d)
	}
}

// Scenario: У каждого поля схемы есть перевод на русский (Ф3).
func TestEveryFieldOfTheSchemaHasARussianTranslation(t *testing.T) {
	for _, field := range fields {
		ru := readSchema(t).Properties[field].I18n["ru"]
		if ru.Title == "" || ru.Description == "" || !strings.ContainsAny(ru.Title+ru.Description, "абвгдежзиклмнопрстуфхцчшщыэюя") {
			t.Errorf("%s: %+v", field, ru)
		}
	}
}

// Scenario: Схема files не требует ни одного секрета.
func TestTheSchemaNamesNoSecret(t *testing.T) {
	for field, p := range readSchema(t).Properties {
		if p.Format == sdk.SecretFormat {
			t.Errorf("%s has format %s", field, p.Format)
		}
	}
	if strings.Contains(string(files.Plugin{}.ConfigSchema()), sdk.SecretFormat) {
		t.Error("the schema mentions the secret format")
	}
}
