// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"fmt"
	"strings"
	"testing"
)

// o is a set of fields that replace those of K; nil removes the field.
type o = map[string]any

func many(n int, f func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

// Scenario: Конфиг, нарушающий схему, отклоняется с указанием поля.
func TestConfigThatBreaksTheSchemaIsRejectedWithTheField(t *testing.T) {
	cases := []struct {
		name, cfg, field string
	}{
		{"empty object", `{}`, "(root)"},
		{"no host", js(k(o{"host": nil})), "(root)"},
		{"no database", js(k(o{"database": nil})), "(root)"},
		{"no user", js(k(o{"user": nil})), "(root)"},
		{"no password_ref", js(k(o{"password_ref": nil})), "(root)"},
		{"password", js(k(o{"password": "x"})), "(root)"},
		{"host empty", js(k(o{"host": ""})), "/host"},
		{"host with a comma", js(k(o{"host": "a,b"})), "/host"},
		{"host with @", js(k(o{"host": "u@db"})), "/host"},
		{"host with spaces", js(k(o{"host": "postgresql   db"})), "/host"},
		{"host as a URL", js(k(o{"host": "postgresql://u:x@db/app"})), "/host"},
		{"host with a slash", js(k(o{"host": "run/postgresql"})), "/host"},
		{"host of 256 bytes", js(k(o{"host": strings.Repeat("h", 256)})), "/host"},
		{"host of 256 bytes in 128 letters", js(k(o{"host": strings.Repeat("ж", 128)})), "/host"},
		{"port 0", js(k(o{"port": 0})), "/port"},
		{"port 65536", js(k(o{"port": 65536})), "/port"},
		{"port as a string", js(k(o{"port": "5432"})), "/port"},
		{"database empty", js(k(o{"database": ""})), "/database"},
		{"database of 64 bytes", js(k(o{"database": strings.Repeat("d", 64)})), "/database"},
		{"database of 64 bytes in 32 letters", js(k(o{"database": strings.Repeat("ж", 32)})), "/database"},
		{"user empty", js(k(o{"user": ""})), "/user"},
		{"user of 64 bytes", js(k(o{"user": strings.Repeat("u", 64)})), "/user"},
		{"host of 256 bytes with 129 characters", js(k(o{"host": strings.Repeat("ж", 127) + "hh"})), "/host"},
		{"database of 64 bytes with 33 characters", js(k(o{"database": strings.Repeat("ж", 31) + "dd"})), "/database"},
		{"user of 64 bytes with 33 characters", js(k(o{"user": strings.Repeat("ж", 31) + "uu"})), "/user"},
		{"exclude_tables pattern of 1025 bytes with 514 characters", js(k(o{"exclude_tables": []string{strings.Repeat("ж", 511) + "abc"}})), "/exclude_tables/0"},
		{"exclude_schemas pattern of 1025 bytes with 514 characters", js(k(o{"exclude_schemas": []string{strings.Repeat("ж", 511) + "abc"}})), "/exclude_schemas/0"},
		{"the second of the patterns is too long", js(k(o{"exclude_tables": []string{"a", strings.Repeat("ж", 513)}})), "/exclude_tables/1"},
		{"user of 64 bytes in 32 letters", js(k(o{"user": strings.Repeat("ж", 32)})), "/user"},
		{"exclude_schemas pattern of 1026 bytes in letters", js(k(o{"exclude_schemas": []string{strings.Repeat("ж", 513)}})), "/exclude_schemas/0"},
		{"password_ref a number", js(k(o{"password_ref": 7})), "/password_ref"},
		{"tls_mode prefer", js(k(o{"tls_mode": "prefer"})), "/tls_mode"},
		{"socket with require", js(k(o{"host": "/var/run/postgresql", "tls_mode": "require"})), "/tls_mode"},
		{"root cert without verify-full", js(k(o{"tls_mode": "require", "tls_root_cert": "/etc/ca.pem"})), "/tls_root_cert"},
		{"root cert without any tls_mode", js(k(o{"tls_mode": nil, "tls_root_cert": "/etc/ca.pem"})), "/tls_root_cert"},
		{"relative root cert", js(k(o{"tls_mode": "verify-full", "tls_root_cert": "ca.pem"})), "/tls_root_cert"},
		{"exclude_schemas a string", js(k(o{"exclude_schemas": "audit"})), "/exclude_schemas"},
		{"exclude_schemas with an empty pattern", js(k(o{"exclude_schemas": []string{""}})), "/exclude_schemas/0"},
		{"exclude_tables repeated", js(k(o{"exclude_tables": []string{"a", "a"}})), "/exclude_tables"},
		{"exclude_tables of 257", js(k(o{"exclude_tables": many(257, func(i int) string { return fmt.Sprint("t", i) })})), "/exclude_tables"},
		{"exclude_tables pattern of 1025 bytes", js(k(o{"exclude_tables": []string{strings.Repeat("p", 1025)}})), "/exclude_tables/0"},
		{"exclude_tables pattern of 1025 bytes in letters", js(k(o{"exclude_tables": []string{strings.Repeat("ж", 513)}})), "/exclude_tables/0"},
		{"relative pg_dump_path", js(k(o{"pg_dump_path": "pg_dump"})), "/pg_dump_path"},
		{"include_globals a string", js(k(o{"include_globals": "yes"})), "/include_globals"},
		{"globals_role_passwords a number", js(k(o{"globals_role_passwords": 1})), "/globals_role_passwords"},
		{"not JSON", `not json`, "(root)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			res := r.run(step(c.cfg))
			want(t, res, rejected)
			mentions(t, res.GetMessage(), c.field)
			if r.proc.started() != 0 || len(r.restic.run) != 0 {
				t.Errorf("processes %d, restic %d", r.proc.started(), len(r.restic.run))
			}
			noOutput(t, res)
		})
	}
}

// Scenario: Строка с символом NUL отклоняется.
func TestStringWithNULIsRejected(t *testing.T) {
	cases := map[string]o{
		"/host":             {"host": "d\x00b"},
		"/database":         {"database": "a\x00pp"},
		"/user":             {"user": "b\x00ackup"},
		"/exclude_tables/0": {"exclude_tables": []string{"t\x00"}},
		"/pg_dump_path":     {"pg_dump_path": "/usr/bin/pg\x00dump"},
	}
	for field, over := range cases {
		r := newRig(t)
		res := r.backup(k(over))
		want(t, res, rejected)
		mentions(t, res.GetMessage(), field)
	}
}

// Scenario: Допустимые значения на границе пределов принимаются схемой.
func TestValuesAtTheLimitsPassTheSchema(t *testing.T) {
	cases := map[string]o{
		"port 1":                            {"port": 1},
		"port 65535":                        {"port": 65535},
		"host ::1":                          {"host": "::1"},
		"host 10.0.0.5":                     {"host": "10.0.0.5"},
		"host db-1.example.com":             {"host": "db-1.example.com"},
		"host of 255 bytes":                 {"host": strings.Repeat("h", 255)},
		"socket and no tls_mode":            {"host": "/var/run/postgresql", "tls_mode": nil},
		"database of 63 bytes":              {"database": strings.Repeat("d", 63)},
		"database of 63 bytes in letters":   {"database": strings.Repeat("ж", 31) + "d"},
		"user of 63 bytes in letters":       {"user": strings.Repeat("ж", 31) + "u"},
		"host of 255 bytes in letters":      {"host": strings.Repeat("ж", 127) + "h"},
		"patterns of 1024 bytes in letters": {"exclude_schemas": []string{strings.Repeat("ж", 512)}, "exclude_tables": []string{strings.Repeat("ж", 512)}},
		"database my db":                    {"database": "my db"},
		"256 patterns":                      {"exclude_schemas": many(256, func(i int) string { return fmt.Sprint("s", i) })},
		"verify-full with root cert":        {"tls_mode": "verify-full", "tls_root_cert": "/etc/ca.pem"},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			res := r.backup(k(over))
			if res.GetStatus() == rejected {
				t.Fatalf("rejected: %s", res.GetMessage())
			}
		})
	}
}

// Scenario: Неизвестный секрет отклоняет шаг до подключения.
func TestUnknownSecretRejectsTheStepBeforeConnecting(t *testing.T) {
	r := newRig(t)
	res := r.backup(k(o{"password_ref": "nope"}))
	want(t, res, rejected)
	mentions(t, res.GetMessage(), "nope")
	if r.proc.started() != 0 {
		t.Errorf("processes started: %d", r.proc.started())
	}
}

// Scenario: Без port используется 5432.
func TestWithoutPortTheDefaultPortIs5432(t *testing.T) {
	r := newRig(t)
	want(t, r.backup(k()), succeeded)
	if got := connection(t, r.proc.ran("psql")[0]).values["port"]; got != "5432" {
		t.Errorf("port = %q", got)
	}
}
