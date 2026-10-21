// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Scenario: Проверка программ PostgreSQL командой check принадлежит плагину
// (R2c-3, В1): hostcheck names no program of a database; the plugin looks for its
// programs itself (sdk.ProgramChecker).
func TestHostcheckNamesNoProgramOfPostgreSQL(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	looked := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		looked++
		for _, named := range programsNamedIn(t, path) {
			t.Error(named)
		}
	}
	if looked < 10 {
		t.Errorf("only %d files were looked at", looked)
	}
}

// programsNamedIn are the places where a string of the file names a program of PostgreSQL.
func programsNamedIn(t *testing.T, path string) (found []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, _ := strconv.Unquote(lit.Value)
			for _, program := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
				if strings.Contains(value, program) {
					found = append(found, fset.Position(lit.Pos()).String()+": the string "+strconv.Quote(value)+" names "+program)
				}
			}
		}
		return true
	})
	return found
}
