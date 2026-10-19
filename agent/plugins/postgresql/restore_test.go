// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Restorer = postgresql.Plugin{}

func restoreSchema(t *testing.T) *pluginhost.Schema {
	t.Helper()
	s, err := pluginhost.CompileSchema("postgresql/restore", postgresql.Plugin{}.RestoreConfigSchema(), func(name string) bool { return name == "pg-restore" })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func kp(extra string) string {
	return `{"host": "db2", "user": "restorer", "password_ref": "pg-restore", "tls_mode": "disable", "database": "app_restored_20261011"` + extra + `}`
}

// Scenario: С R2b плагин postgresql объявляет схему восстановления.
func TestTheRestoreSchemaHasExactlyTheFieldsOfARestoreIntoANewDatabase(t *testing.T) {
	var doc schemaDoc
	if err := json.Unmarshal(postgresql.Plugin{}.RestoreConfigSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"host", "port", "user", "password_ref", "tls_mode", "tls_root_cert", "database", "restore_globals", "pg_restore_path"}
	if len(doc.Properties) != len(want) || doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Errorf("properties %d, want exactly %v and no more", len(doc.Properties), want)
	}
	if p := doc.Properties["password_ref"]; p.Format != "sard-secret" {
		t.Errorf("password_ref format %q", p.Format)
	}
}

func TestEveryFieldOfTheRestoreSchemaHasATitleADescriptionAndATranslation(t *testing.T) {
	var doc schemaDoc
	if err := json.Unmarshal(postgresql.Plugin{}.RestoreConfigSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	for field, p := range doc.Properties {
		ru := p.I18n["ru"]
		if p.Title == "" || p.Description == "" || len(p.Examples) == 0 || ru.Title == "" || ru.Description == "" {
			t.Errorf("%s: %+v", field, p)
		}
	}
}

// Scenario: Недопустимый конфиг восстановления postgresql отклоняется по схеме.
func TestTheRestoreSchemaRejectsAnInvalidConfig(t *testing.T) {
	schema := restoreSchema(t)
	if err := schema.Validate([]byte(kp(""))); err != nil {
		t.Fatalf("KP: %v", err)
	}
	for name, tt := range map[string]struct{ cfg, field string }{
		"no database":     {`{"host": "db2", "user": "r", "password_ref": "pg-restore"}`, "database"},
		"64 bytes":        {strings.Replace(kp(""), "app_restored_20261011", strings.Repeat("a", 64), 1), "database"},
		"NUL":             {strings.Replace(kp(""), "app_restored_20261011", `a\u0000b`, 1), "database"},
		"a password":      {kp(`, "password": "p"`), "password"},
		"unknown secret":  {strings.Replace(kp(""), "pg-restore", "nope", 1), "password_ref"},
		"globals as text": {kp(`, "restore_globals": "yes"`), "restore_globals"},
		"overwrite":       {kp(`, "overwrite": true`), "overwrite"},
	} {
		err := schema.Validate([]byte(tt.cfg))
		var cfgErr *sdk.ConfigError
		if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), tt.field) {
			t.Errorf("%s: Validate = %v, want a violation naming %s", name, err, tt.field)
		}
	}
}

func TestThePlanOfARestoreIntoADatabaseCarriesTheConnectionAndTheChoices(t *testing.T) {
	plan, err := postgresql.Plugin{}.PlanRestore([]byte(kp(`, "port": 6432, "tls_root_cert": "/etc/ca.pem", "restore_globals": true, "pg_restore_path": "/opt/pg/bin/pg_restore"`)))
	want := sdk.DatabaseRestore{Host: "db2", Port: 6432, User: "restorer", PasswordRef: "pg-restore", TLSMode: "disable", TLSRootCert: "/etc/ca.pem",
		Name: "app_restored_20261011", Globals: true, ClientPath: "/opt/pg/bin/pg_restore"}
	if err != nil || plan.Database == nil || *plan.Database != want || plan.Destination != "" || plan.Paths != nil {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}

func TestThePlanOfARestoreDefaultsThePortAndLeavesGlobalsOff(t *testing.T) {
	plan, err := postgresql.Plugin{}.PlanRestore([]byte(kp("")))
	if err != nil || plan.Database.Port != 5432 || plan.Database.Globals || plan.Database.ClientPath != "" {
		t.Fatalf("plan = %+v, %v", plan.Database, err)
	}
}

// Scenario: Недопустимый конфиг восстановления postgresql отклоняется по схеме (the schema counts characters, the plan bytes).
func TestAPlanOfARestoreCountsTheBytesOfTheDatabaseName(t *testing.T) {
	name := strings.Repeat("ж", 32)
	_, err := postgresql.Plugin{}.PlanRestore([]byte(strings.Replace(kp(""), "app_restored_20261011", name, 1)))
	if !errors.Is(err, sdk.ErrInvalidConfig) || !strings.Contains(err.Error(), "/database") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (postgresql.Plugin{}).PlanRestore([]byte(`not json`)); !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

func TestAPlanOfARestoreKeepsTheLimitsInBytesOfTheHostAndTheRole(t *testing.T) {
	long := strings.Repeat("ж", 100) // 200 bytes
	_, err := postgresql.Plugin{}.PlanRestore([]byte(strings.Replace(kp(""), `"user": "restorer"`, `"user": "`+long[:64]+`"`, 1)))
	if !errors.Is(err, sdk.ErrInvalidConfig) || !strings.Contains(err.Error(), "/user") {
		t.Fatalf("err = %v", err)
	}
}
