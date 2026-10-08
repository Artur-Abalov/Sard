// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Everything the connection does to the host or to time comes in through
// the Connector (Р26): the package imports no way out of the process and
// calls neither the file system nor the clock itself.
var (
	forbiddenImports = []string{"os/exec", "net", "syscall", "crypto/rand"}
	forbiddenOSCalls = []string{
		"Open", "OpenFile", "Create", "CreateTemp", "ReadFile", "WriteFile", "ReadDir", "Mkdir", "MkdirAll", "MkdirTemp",
		"Remove", "RemoveAll", "Rename", "Stat", "Lstat", "Chmod", "Chown", "Lchown", "Link", "Symlink", "Readlink",
		"Truncate", "Getenv", "Environ", "Exit", "OpenRoot",
	}
	forbiddenTimeCalls = []string{"Now", "After", "Sleep", "NewTimer", "Tick", "NewTicker", "AfterFunc", "Since", "Until"}
)

// parsed are the non-test files of the package.
func parsed(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("files %v, %v", files, err)
	}
	fset, got := token.NewFileSet(), map[string]*ast.File{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		got[name] = file
	}
	return fset, got
}

func TestThePackageImportsNoWayOutOfTheProcess(t *testing.T) {
	_, files := parsed(t)
	for name, file := range files {
		for _, imp := range file.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); slices.Contains(forbiddenImports, path) {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

func TestThePackageCallsNeitherTheFileSystemNorTheClockDirectly(t *testing.T) {
	fset, files := parsed(t)
	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Obj == nil && forbiddenCall(pkg.Name, sel.Sel.Name) {
					t.Errorf("%s: %s calls %s.%s directly", name, fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func forbiddenCall(pkg, name string) bool {
	return pkg == "os" && slices.Contains(forbiddenOSCalls, name) || pkg == "time" && slices.Contains(forbiddenTimeCalls, name)
}
