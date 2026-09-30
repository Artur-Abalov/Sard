// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// The lock reaches the file system only through the OpenFunc it is given
// (the seam that lets tests refuse a create as the kernel does for a
// non-root user); only WriteNew in password.go keeps its own os.OpenFile.
func TestOnlyThePasswordWriterOpensFilesDirectly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "password.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && isOSOpenFile(sel) {
				t.Errorf("%s references os.OpenFile", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

func isOSOpenFile(sel *ast.SelectorExpr) bool {
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "os" && sel.Sel.Name == "OpenFile"
}
