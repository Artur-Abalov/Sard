// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restickeys_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// restickeys decides; it never reaches the host, the network, the clock or
// the randomness (they are the commands' business, passed in). The package
// may use only restic.Key of the restic package.

const resticImport = "github.com/Artur-Abalov/sard/agent/internal/restic"

func productionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	files := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = f
	}
	return files
}

func importName(spec *ast.ImportSpec, path string) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	return filepath.Base(path)
}

func TestTheDecisionsImportNothingThatReachesTheHost(t *testing.T) {
	t.Parallel()
	forbidden := map[string]bool{"os": true, "os/exec": true, "net": true, "syscall": true, "io/fs": true, "crypto/rand": true, "time": true}
	for name, f := range productionFiles(t) {
		for _, spec := range f.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if forbidden[path] {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

// resticName is the name the file gives the restic package, "" if it does not import it.
func resticName(f *ast.File) string {
	for _, spec := range f.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == resticImport {
			return importName(spec, path)
		}
	}
	return ""
}

// resticUses are the names the file takes from the package named local.
func resticUses(f *ast.File, local string) []string {
	var used []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, isID := sel.X.(*ast.Ident); isID && id.Name == local {
			used = append(used, sel.Sel.Name)
		}
		return true
	})
	return used
}

func TestTheDecisionsUseOnlyTheKeyOfRestic(t *testing.T) {
	t.Parallel()
	for name, f := range productionFiles(t) {
		local := resticName(f)
		if local == "" {
			continue
		}
		for _, used := range resticUses(f, local) {
			if used != "Key" {
				t.Errorf("%s uses restic.%s", name, used)
			}
		}
	}
}
