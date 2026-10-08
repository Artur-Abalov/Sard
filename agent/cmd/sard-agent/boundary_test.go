// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// selectors calls visit for every pkg.Name selector of the non-test files.
func selectors(t *testing.T, visit func(file string, pos token.Position, pkg, name string)) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("files %v, %v", files, err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					visit(name, fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// The decisions of the connection live in internal/repoconnect (Р26): the
// command line only wires them to flags, output and the host.
func TestPackageMainDoesNotDecideTheConnection(t *testing.T) {
	forbidden := map[string]bool{
		"hostsetup.S3EnvIdentity": true, "hostsetup.S3EnvFile": true,
		"restic.ParseEnvFile": true, "repoinit.NewPassword": true,
	}
	selectors(t, func(file string, pos token.Position, pkg, name string) {
		if forbidden[pkg+"."+name] {
			t.Errorf("%s: %s references %s.%s", file, pos, pkg, name)
		}
	})
}
