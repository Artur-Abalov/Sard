// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package internal_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The agent holds no code of a concrete database (ADR 0066, the principle of the
// owner): what talks to PostgreSQL lives in the plugin. The rule holds for the
// whole of agent/internal and for the root of the build, agent/cmd/sard-agent
// (В2); the plugins are where the code of a database belongs.

// scope are the directories the rule holds for: agent/internal, where the test
// runs, and the root of the build.
var scope = []string{".", filepath.Join("..", "cmd", "sard-agent")}

const pgclientImport = "/pgclient"

var (
	// affixes are the starts and ends of a string that is a tag, a reason or a
	// tool of PostgreSQL.
	affixes = []string{"postgresql.", "PG_", "pg_restore"}
	// codes are the reasons of the restore of a database that have no PG_ in them (О4).
	codes = []string{"CREATEDB_REQUIRED", "SUPERUSER_REQUIRED", "GLOBALS_SNAPSHOT_MISSING", "GLOBALS_TABLESPACES", "ROLES_EXIST", "DATABASE_EXISTS"}
)

// violations are the places of the source where it knows PostgreSQL: the
// import of a client package, a string that starts or ends with one of the
// affixes, a string that holds one of the codes. Comments are not code. src is
// the text of the file, nil to read it.
func violations(name string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	report := func(pos token.Pos, what string) {
		found = append(found, fmt.Sprintf("%s: %s", fset.Position(pos), what))
	}
	for _, imp := range file.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); strings.HasSuffix(path, pgclientImport) {
			report(imp.Pos(), "imports "+path)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if _, isImport := n.(*ast.ImportSpec); isImport {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok {
			if what := knowsLiteral(lit); what != "" {
				report(lit.Pos(), what)
			}
		}
		return true
	})
	return found, nil
}

// knowsLiteral says what in the literal is PostgreSQL's, "" if nothing or if it is no string.
func knowsLiteral(lit *ast.BasicLit) string {
	value, err := strconv.Unquote(lit.Value)
	if lit.Kind != token.STRING || err != nil {
		return ""
	}
	return knows(value)
}

// knows says what in the string is PostgreSQL's, "" if nothing.
func knows(value string) string {
	for _, a := range affixes {
		if strings.HasPrefix(value, a) || strings.HasSuffix(value, a) {
			return fmt.Sprintf("the string %q starts or ends with %q", value, a)
		}
	}
	for _, c := range codes {
		if strings.Contains(value, c) {
			return fmt.Sprintf("the string %q holds the reason %s", value, c)
		}
	}
	return ""
}

// sources are the Go files below the roots that are neither tests nor test data.
func sources(roots ...string) ([]string, error) {
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir() && d.Name() == "testdata":
				return fs.SkipDir
			case !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// scan is the violations of every source below the roots and how many files it looked at.
func scan(roots ...string) (found []string, files int, err error) {
	paths, err := sources(roots...)
	if err != nil {
		return nil, 0, err
	}
	for _, path := range paths {
		v, err := violations(path, nil)
		if err != nil {
			return nil, 0, err
		}
		found = append(found, v...)
	}
	return found, len(paths), nil
}

// Scenario: Код агента вне плагинов не знает PostgreSQL.
func TestTheAgentOutsideThePluginsHoldsNoCodeOfPostgreSQL(t *testing.T) {
	found, files, err := scan(scope...)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range found {
		t.Error(v)
	}
	if files < 100 {
		t.Errorf("only %d files were looked at: the scope %v is not where the code is", files, scope)
	}
}

// Scenario: Архитектурный тест ловит подложенное нарушение.
func TestThePlantedViolationsAreFoundWithTheFileAndTheLine(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "nopostgres", "violations", "*.go"))
	if err != nil || len(files) < 10 {
		t.Fatalf("fixtures %v, %v", files, err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			found, err := violations(path, nil)
			if err != nil || len(found) != 1 {
				t.Fatalf("found %v, %v", found, err)
			}
			if !strings.HasPrefix(found[0], path+":6:") {
				t.Errorf("the file and the line are not named: %s", found[0])
			}
		})
	}
}

// Scenario: Архитектурный тест не путает допустимые строки с кодом базы.
func TestWhatIsNoCodeOfADatabaseIsNotFound(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "nopostgres", "allowed", "*.go"))
	if err != nil || len(files) < 4 {
		t.Fatalf("fixtures %v, %v", files, err)
	}
	for _, path := range files {
		if found, err := violations(path, nil); err != nil || len(found) != 0 {
			t.Errorf("%s: found %v, %v", path, found, err)
		}
	}
}

func TestEveryCodeOfTheRestoreOfADatabaseIsFound(t *testing.T) {
	for _, code := range codes {
		found, err := violations("x.go", "package x; var _ = \""+code+"\"")
		if err != nil || len(found) != 1 {
			t.Errorf("%s: found %v, %v", code, found, err)
		}
	}
	for _, affix := range affixes {
		for _, text := range []string{affix + "x", "x" + affix} {
			if found, err := violations("x.go", "package x; var _ = \""+text+"\""); err != nil || len(found) != 1 {
				t.Errorf("%s: found %v, %v", text, found, err)
			}
		}
	}
}

// Scenario: Тестовые файлы агента архитектурный тест не проверяет.
func TestTestFilesAndTestDataAreNotScanned(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("fine.go", "package x\n")
	write("fine_test.go", "package x\nvar _ = \"pg_restore\"\n")
	write("testdata/planted.go", "package x\nvar _ = \"pg_restore\"\n")
	found, files, err := scan(dir)
	if err != nil || len(found) != 0 || files != 1 {
		t.Fatalf("found %v in %d files, %v", found, files, err)
	}
	write("sub/bad.go", "package x\nvar _ = \"pg_restore\"\n")
	if found, files, err = scan(dir); err != nil || len(found) != 1 || files != 2 {
		t.Fatalf("found %v in %d files, %v", found, files, err)
	}
}
