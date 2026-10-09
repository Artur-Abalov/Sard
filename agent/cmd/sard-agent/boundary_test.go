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

// The decisions of the sftp: connection (the host key, the key, known_hosts,
// the block of the config, the class of a refused login) live in
// internal/repoconnect; package main only wires them to flags, output and
// the host (A8b-2).
func TestPackageMainDoesNotDecideTheSSHSetup(t *testing.T) {
	forbidden := map[string]bool{
		"repoconnect.FindKnown": true, "repoconnect.AddHostKey": true, "repoconnect.ReplaceHostKey": true,
		"repoconnect.ParseKeyscan": true, "hostsetup.OpenSSHHome": true, "repoconnect.ApplyBlock": true, "repoconnect.ManagedBlock": true,
	}
	selectors(t, func(file string, pos token.Position, pkg, name string) {
		if forbidden[pkg+"."+name] {
			t.Errorf("%s: %s references %s.%s", file, pos, pkg, name)
		}
	})
}

func TestPackageMainImportsNothingToHandleKeysOrFingerprints(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("files %v, %v", files, err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			switch path := strings.Trim(imp.Path.Value, `"`); path {
			case "crypto/sha256", "crypto/sha1", "crypto/hmac", "encoding/base64":
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

// stringLiterals calls visit for every string literal of the non-test files.
func stringLiterals(t *testing.T, visit func(file string, pos token.Position, value string)) {
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
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				visit(name, fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}

// The table of providers of --provider lives in hostsetup (Р50): package
// main passes the flags to hostsetup.ExpandProvider and holds no host of a
// provider; only the help text shows the templates.
func TestPackageMainHoldsNoHostOfAProvider(t *testing.T) {
	stringLiterals(t, func(file string, pos token.Position, value string) {
		holds := strings.Contains(value, "amazonaws.com") || strings.Contains(value, "backblazeb2.com")
		if holds && file != "host_help.go" {
			t.Errorf("%s: %s holds a host of a provider", file, pos)
		}
	})
}
