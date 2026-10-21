// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	sardPrefix = "github.com/Artur-Abalov/sard/"
	sdkPackage = sardPrefix + "agent/plugins/sdk"
)

// outsideTheSDK reports whether pkg is a package of Sard that is not the SDK.
func outsideTheSDK(pkg string) bool {
	return strings.HasPrefix(pkg, sardPrefix) && pkg != sdkPackage && !strings.HasPrefix(pkg, sdkPackage+"/")
}

// Scenario: SDK не зависит от сгенерированного кода протокола. The SDK is
// Apache-2.0: it depends on no package of Sard but itself, so neither on an AGPL
// module nor on the generated code of proto/gen/go (ADR 0066).
func TestSDKDoesNotImportAnyPackageOfSardOutsideItself(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if outsideTheSDK(pkg) {
			t.Errorf("SDK depends on %s", pkg)
		}
	}
}

func TestTheRuleOfTheSDKImportsNamesTheGeneratedCode(t *testing.T) {
	for pkg, want := range map[string]bool{
		sardPrefix + "proto/gen/go/sard/v1":     true,
		sardPrefix + "agent/internal/restic":    true,
		sardPrefix + "agent/plugins/postgresql": true,
		sdkPackage:                              false,
		sdkPackage + "/sub":                     false,
		"google.golang.org/grpc":                false,
		"fmt":                                   false,
	} {
		if got := outsideTheSDK(pkg); got != want {
			t.Errorf("outsideTheSDK(%q) = %v, want %v", pkg, got, want)
		}
	}
}

// sourceFiles are the Go files of the directory that are not tests.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		}
	}
	return names
}

// exportedNames are the exported names the file declares, with their places.
func exportedNames(t *testing.T, path string) (names, positions []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.IsExported() && id.Obj != nil {
			names = append(names, id.Name)
			positions = append(positions, fset.Position(id.Pos()).String())
		}
		return true
	})
	return names, positions
}

// Scenario: SDK не содержит типов конкретной базы данных.
func TestSDKHoldsNoTypeOfAConcreteDatabase(t *testing.T) {
	if _, err := os.Stat("pgclient"); err == nil {
		t.Error("the SDK holds a package pgclient")
	}
	seen := 0
	for _, path := range sourceFiles(t) {
		names, positions := exportedNames(t, path)
		seen += len(names)
		for i, name := range names {
			if strings.Contains(strings.ToLower(name), "postgres") {
				t.Errorf("%s: the exported name %s", positions[i], name)
			}
		}
	}
	if seen < 20 {
		t.Errorf("only %d exported names were looked at", seen)
	}
}
