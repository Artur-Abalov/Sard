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

// The umask belongs to the process: a test that changes it while parallel tests run makes their files and
// directories with another mode (a home of mode 0777 refused as SSH_HOME_INVALID). Such a test is not parallel.
func TestATestThatChangesTheUmaskIsNotParallel(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range parallelUmaskTests(file) {
			t.Errorf("%s: %s changes the umask and runs in parallel", fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
}

// parallelUmaskTests are the tests of file that call both syscall.Umask and t.Parallel.
func parallelUmaskTests(file *ast.File) []*ast.FuncDecl {
	var found []*ast.FuncDecl
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Body == nil {
			continue
		}
		if calls(fn.Body, "syscall", "Umask") && calls(fn.Body, "t", "Parallel") {
			found = append(found, fn)
		}
	}
	return found
}

// calls reports whether body calls receiver.name anywhere, function literals included.
func calls(body ast.Node, receiver, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return !found
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == name {
			if id, isIdent := sel.X.(*ast.Ident); isIdent && id.Name == receiver {
				found = true
			}
		}
		return !found
	})
	return found
}
