// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package crap

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// GoFunc is a top-level function or method found in Go source.
type GoFunc struct {
	File      string // slash-separated path relative to the module root
	Name      string // Func or Recv.Method
	CC        int
	StartLine int
	EndLine   int
}

// Complexity returns the cyclomatic complexity of a function body the way
// gocyclo counts it: 1 + if, for, range, non-default case, non-default
// comm clause, && and ||. Function literals count toward the enclosing
// function.
func Complexity(fn *ast.FuncDecl) int {
	cc := 1
	ast.Inspect(fn, func(n ast.Node) bool {
		cc += branches(n)
		return true
	})
	return cc
}

func branches(n ast.Node) int {
	switch n := n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
		return 1
	case *ast.CaseClause:
		return oneIf(n.List != nil)
	case *ast.CommClause:
		return oneIf(n.Comm != nil)
	case *ast.BinaryExpr:
		return oneIf(n.Op == token.LAND || n.Op == token.LOR)
	}
	return 0
}

func oneIf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ScanGo parses every non-test, non-generated .go file under root and
// returns its functions. Directories named vendor or testdata and hidden
// directories are skipped.
func ScanGo(root string) ([]GoFunc, error) {
	s := &scanner{root: root, fset: token.NewFileSet()}
	err := filepath.WalkDir(root, s.visit)
	return s.funcs, err
}

type scanner struct {
	root  string
	fset  *token.FileSet
	funcs []GoFunc
}

func (s *scanner) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if d.IsDir() {
		return skipDir(path, s.root, d.Name())
	}
	if !isMeasuredGoFile(d.Name()) {
		return nil
	}
	return s.scanFile(path)
}

func (s *scanner) scanFile(path string) error {
	f, err := parser.ParseFile(s.fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}
	if ast.IsGenerated(f) {
		return nil
	}
	rel, _ := filepath.Rel(s.root, path) // cannot fail: WalkDir paths are below root
	s.funcs = append(s.funcs, fileFuncs(s.fset, f, filepath.ToSlash(rel))...)
	return nil
}

func skipDir(path, root, name string) error {
	if path == root {
		return nil
	}
	if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	return nil
}

func isMeasuredGoFile(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func fileFuncs(fset *token.FileSet, f *ast.File, rel string) []GoFunc {
	var funcs []GoFunc
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcs = append(funcs, GoFunc{
			File:      rel,
			Name:      funcName(fn),
			CC:        Complexity(fn),
			StartLine: fset.Position(fn.Pos()).Line,
			EndLine:   fset.Position(fn.End()).Line,
		})
	}
	return funcs
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil {
		return fn.Name.Name
	}
	return recvType(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func recvType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvType(t.X)
	case *ast.IndexExpr:
		return recvType(t.X)
	case *ast.IndexListExpr:
		return recvType(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
