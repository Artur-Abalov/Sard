// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// The identifiers of the checks belong to hostcheck (Ч10): package main
// takes them from there and never spells one out.
func TestPackageMainDeclaresNoCheckIdentifier(t *testing.T) {
	ids := append(hostcheck.CheckIDs(), hostcheck.CheckRepositoryIntegrity)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
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
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(lit.Value); err == nil && slices.Contains(ids, value) {
				t.Errorf("%s declares the check identifier %q", fset.Position(lit.Pos()), value)
			}
			return true
		})
	}
}
