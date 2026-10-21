// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package internal_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The agent holds no code of a concrete database (ADR 0066, the principle of the
// owner): what talks to PostgreSQL lives in the plugin. R2c-2 proves it for the
// restore from the console and the host of the plugins; R2c-3 widens the scope
// to the whole of agent/internal.

// scope are the directories, below agent/internal, the rule holds for.
var scope = []string{"consolerestore", "pluginhost"}

const pgclientImport = "/plugins/sdk/pgclient"

var (
	// affixes are the starts and ends of a string that is a tag, a reason or a
	// tool of PostgreSQL.
	affixes = []string{"postgresql.", "PG_", "pg_restore"}
	// codes are the reasons of the restore of a database that have no PG_ in them.
	codes = []string{"CREATEDB_REQUIRED", "SUPERUSER_REQUIRED", "GLOBALS_SNAPSHOT_MISSING", "GLOBALS_TABLESPACES", "ROLES_EXIST", "DATABASE_EXISTS"}
)

// violations are the places of the source where it knows PostgreSQL: the
// import of the client package, a string that starts or ends with one of the
// affixes, a string that holds one of the codes. Comments are not code.
func violations(name string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	report := func(pos token.Pos, what string) {
		found = append(found, fmt.Sprintf("%s: %s", fset.Position(pos), what))
	}
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); strings.HasSuffix(path, pgclientImport) {
			report(imp.Pos(), "imports "+path)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if _, isImport := n.(*ast.ImportSpec); isImport {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok {
			if what := knowsLiteral(lit); what != "" {
				report(lit.Pos(), what)
			}
		}
		return true
	})
	return found, nil
}

// knowsLiteral says what in the literal is PostgreSQL's, "" if nothing or if it is no string.
func knowsLiteral(lit *ast.BasicLit) string {
	value, err := strconv.Unquote(lit.Value)
	if lit.Kind != token.STRING || err != nil {
		return ""
	}
	return knows(value)
}

// knows says what in the string is PostgreSQL's, "" if nothing.
func knows(value string) string {
	for _, a := range affixes {
		if strings.HasPrefix(value, a) || strings.HasSuffix(value, a) {
			return fmt.Sprintf("the string %q starts or ends with %q", value, a)
		}
	}
	for _, c := range codes {
		if strings.Contains(value, c) {
			return fmt.Sprintf("the string %q holds the reason %s", value, c)
		}
	}
	return ""
}

// sources are the files of the scope that are not tests.
func sources(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, dir := range scope {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// Scenario: В восстановлении из консоли агента нет кода PostgreSQL.
func TestTheRestoreFromTheConsoleOfTheAgentHoldsNoCodeOfPostgreSQL(t *testing.T) {
	files := sources(t)
	for _, path := range files {
		found, err := violations(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range found {
			t.Error(v)
		}
	}
	if len(files) < 10 {
		t.Errorf("only %d files were looked at: the scope %v is not where the code is", len(files), scope)
	}
}

func TestTheCheckFindsWhatItLooksForAndNothingElse(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		count int
	}{
		{"the client package", `package x; import _ "github.com/Artur-Abalov/sard/agent/plugins/sdk/pgclient"`, 1},
		{"a tag", `package x; var _ = "postgresql.part"`, 1},
		{"a tag after a word", `package x; var _ = "tag postgresql."`, 1},
		{"a reason", `package x; var _ = "PG_CONNECT"`, 1},
		{"the restore tool", `package x; var _ = "pg_restore"`, 1},
		{"the restore tool in a command", `package x; var _ = "run pg_restore"`, 1},
		{"a reason without PG_", `package x; var _ = "ROLES_EXIST"`, 1},
		{"a reason in a sentence", `package x; var _ = "the reason is DATABASE_EXISTS here"`, 1},
		{"every code", `package x; var _ = []string{"CREATEDB_REQUIRED", "SUPERUSER_REQUIRED", "GLOBALS_SNAPSHOT_MISSING", "GLOBALS_TABLESPACES"}`, 4},
		{"the key of the setting", `package x; var _ = "restore.postgresql_lock_share"`, 0},
		{"the name of the plugin", `package x; var _ = "postgresql"`, 0},
		{"a comment", "package x\n// pg_restore, PG_CONNECT, postgresql.part\nvar _ = 1", 0},
		{"an unrelated import", `package x; import _ "github.com/Artur-Abalov/sard/agent/plugins/sdk"`, 0},
		{"a raw string", "package x; var _ = `pg_restore`", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := violations("testdata.go", tt.src)
			if err != nil || len(found) != tt.count {
				t.Fatalf("found %v, %v; want %d", found, err, tt.count)
			}
			for _, v := range found {
				if !strings.HasPrefix(v, "testdata.go:1:") {
					t.Errorf("the place is not named: %s", v)
				}
			}
		})
	}
}
