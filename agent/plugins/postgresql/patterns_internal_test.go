// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"slices"
	"strings"
	"testing"
)

// The cases follow patternToSQLRegex of PostgreSQL (fe_utils/string_utils.c),
// with force_escape off as pg_dump calls it.
func TestNamePatternsAreSplitAndTranslatedAsPsqlDoes(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"log_*", []string{"^(log_.*)$"}},
		{"Public.Users", []string{"^(public)$", "^(users)$"}},
		{`"Public"."Users"`, []string{"^(Public)$", "^(Users)$"}},
		{`"a.b"`, []string{`^(a\.b)$`}},
		{`"say ""hi"""`, []string{`^(say "hi")$`}},
		{"a?c", []string{"^(a.c)$"}},
		{"cost$", []string{`^(cost\$)$`}},
		{`"x*y"`, []string{`^(x\*y)$`}},
		{"a|b", []string{"^(a|b)$"}},
		{"public.", []string{"^(public)$", "^()$"}},
		{"db.public.t", []string{"^(db)$", "^(public)$", "^(t)$"}},
		{"Ж", []string{"^(Ж)$"}},
	} {
		if got := patternParts(tc.pattern); !slices.Equal(got, tc.want) {
			t.Errorf("patternParts(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func TestATablePatternIsAConditionOnTheSchemaAndTheName(t *testing.T) {
	for _, tc := range []struct {
		pattern, want string
	}{
		{"log_*", `(c.relname OPERATOR(pg_catalog.~) '^(log_.*)$' COLLATE pg_catalog.default AND pg_catalog.pg_table_is_visible(c.oid))`},
		{"audit.x", `(c.relname OPERATOR(pg_catalog.~) '^(x)$' COLLATE pg_catalog.default AND n.nspname OPERATOR(pg_catalog.~) '^(audit)$' COLLATE pg_catalog.default)`},
		{"audit.", `(n.nspname OPERATOR(pg_catalog.~) '^(audit)$' COLLATE pg_catalog.default)`},
		{".x", `(c.relname OPERATOR(pg_catalog.~) '^(x)$' COLLATE pg_catalog.default AND pg_catalog.pg_table_is_visible(c.oid))`},
		{"db.s.t", `(c.relname OPERATOR(pg_catalog.~) '^(t)$' COLLATE pg_catalog.default AND n.nspname OPERATOR(pg_catalog.~) '^(s)$' COLLATE pg_catalog.default)`},
		{"it's", `(c.relname OPERATOR(pg_catalog.~) '^(it''s)$' COLLATE pg_catalog.default AND pg_catalog.pg_table_is_visible(c.oid))`},
		{".", `(pg_catalog.pg_table_is_visible(c.oid))`},
	} {
		got, err := tableCondition(tc.pattern)
		if err != nil || got != tc.want {
			t.Errorf("tableCondition(%q) = %s, %v\nwant %s", tc.pattern, got, err, tc.want)
		}
	}
}

func TestASchemaPatternIsAConditionOnTheSchema(t *testing.T) {
	for _, tc := range []struct{ pattern, want string }{
		{"audit_*", `(n.nspname OPERATOR(pg_catalog.~) '^(audit_.*)$' COLLATE pg_catalog.default)`},
		{"db.audit", `(n.nspname OPERATOR(pg_catalog.~) '^(audit)$' COLLATE pg_catalog.default)`},
		{"db.", `(true)`},
	} {
		got, err := schemaCondition(tc.pattern)
		if err != nil || got != tc.want {
			t.Errorf("schemaCondition(%q) = %s, %v\nwant %s", tc.pattern, got, err, tc.want)
		}
	}
}

// pg_dump refuses more dotted names than it reads; so does the fingerprint.
func TestTooManyDottedNamesAreRefused(t *testing.T) {
	if _, err := tableCondition("a.b.c.d"); err == nil || !strings.Contains(err.Error(), "too many dotted names") {
		t.Errorf("table: %v", err)
	}
	if _, err := schemaCondition("a.b.c"); err == nil || !strings.Contains(err.Error(), "too many dotted names") {
		t.Errorf("schema: %v", err)
	}
}

func TestTheExcludedTablesAreOneConditionOfAllPatterns(t *testing.T) {
	got, err := excludedCondition(config{ExcludeSchemas: []string{"audit"}, ExcludeTables: []string{"log"}})
	want := `((n.nspname OPERATOR(pg_catalog.~) '^(audit)$' COLLATE pg_catalog.default) OR (c.relname OPERATOR(pg_catalog.~) '^(log)$' COLLATE pg_catalog.default AND pg_catalog.pg_table_is_visible(c.oid)))`
	if err != nil || got != want {
		t.Errorf("excludedCondition = %s, %v", got, err)
	}
	if got, err := excludedCondition(config{}); err != nil || got != "false" {
		t.Errorf("no patterns: %s, %v", got, err)
	}
	if _, err := excludedCondition(config{ExcludeTables: []string{"a.b.c.d"}}); err == nil {
		t.Error("a bad pattern passed")
	}
}
