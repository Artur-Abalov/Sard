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
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, _ := strconv.Unquote(lit.Value)
			for _, program := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
				if strings.Contains(value, program) {
					t.Errorf("%s: the string %q names %s", fset.Position(lit.Pos()), value, program)
				}
			}
			return true
		})
	}
	if looked < 10 {
		t.Errorf("only %d files were looked at", looked)
	}
}
