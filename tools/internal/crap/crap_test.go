// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package crap

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScore(t *testing.T) {
	cases := []struct {
		cc   int
		cov  float64
		want float64
	}{
		{1, 1, 1},
		{1, 0, 2},
		{5, 0, 30},
		{5, 0.5, 8.125},
		{10, 1, 10},
	}
	for _, c := range cases {
		if got := Score(c.cc, c.cov); got != c.want {
			t.Errorf("Score(%d, %v) = %v, want %v", c.cc, c.cov, got, c.want)
		}
	}
}

func TestSortOrdersByCRAPThenName(t *testing.T) {
	rows := []Row{{"b", 1, 1}, {"c", 5, 0}, {"a", 1, 1}}
	Sort(rows)
	got := []string{rows[0].Name, rows[1].Name, rows[2].Name}
	if strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("order = %v", got)
	}
}

func TestOffendersAreStrictlyAboveThreshold(t *testing.T) {
	rows := []Row{{"hi", 5, 0}, {"eq", 6, 1}, {"lo", 1, 1}}
	Sort(rows)
	off := Offenders(rows, 6)
	if len(off) != 1 || off[0].Name != "hi" {
		t.Fatalf("offenders = %+v", off)
	}
	if len(Offenders(nil, 6)) != 0 {
		t.Fatal("offenders of empty")
	}
}

func TestWriteLimitsRows(t *testing.T) {
	var buf bytes.Buffer
	rows := []Row{{"one", 2, 0.5}, {"two", 1, 1}}
	if err := Write(&buf, rows, 1); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "     2.5    2   50.0%  one") || strings.Contains(out, "two") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	buf.Reset()
	if err := Write(&buf, rows, 0); err != nil || !strings.Contains(buf.String(), "two") {
		t.Fatalf("top=0 must print all rows: %q %v", buf.String(), err)
	}
}

type failWriter struct{ after int }

func (w *failWriter) Write(p []byte) (int, error) {
	if w.after == 0 {
		return 0, errors.New("boom")
	}
	w.after--
	return len(p), nil
}

func TestWritePropagatesErrors(t *testing.T) {
	rows := []Row{{"one", 1, 1}}
	if Write(&failWriter{0}, rows, 0) == nil {
		t.Error("header error lost")
	}
	if Write(&failWriter{1}, rows, 0) == nil {
		t.Error("row error lost")
	}
}

const jacocoXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="server">
 <package name="dev/sard/server">
  <class name="dev/sard/server/Status">
   <method name="version" desc="()Ljava/lang/String;">
    <counter type="INSTRUCTION" missed="1" covered="3"/>
    <counter type="COMPLEXITY" missed="1" covered="1"/>
   </method>
   <method name="noComplexity" desc="()V">
    <counter type="INSTRUCTION" missed="0" covered="3"/>
   </method>
   <method name="empty" desc="()V">
    <counter type="COMPLEXITY" missed="0" covered="1"/>
   </method>
  </class>
 </package>
</report>`

func TestParseJaCoCo(t *testing.T) {
	rows, err := ParseJaCoCo(strings.NewReader(jacocoXML))
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{"dev.sard.server.Status.version()Ljava/lang/String;", 2, 0.75},
		{"dev.sard.server.Status.empty()V", 1, 0},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

func TestParseJaCoCoErrors(t *testing.T) {
	if _, err := ParseJaCoCo(strings.NewReader("<report>")); err == nil || !strings.Contains(err.Error(), "parse jacoco xml") {
		t.Errorf("malformed xml: err = %v", err)
	}
	_, err := ParseJaCoCo(strings.NewReader(`<report><package><class name="a/B"><method name="m" desc="()V"/></class></package></report>`))
	if !errors.Is(err, ErrNoComplexity) {
		t.Errorf("err = %v, want ErrNoComplexity", err)
	}
}

func parseFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f.Decls[0].(*ast.FuncDecl)
}

func TestComplexity(t *testing.T) {
	cases := map[string]int{
		`func f() {}`: 1,
		`func f(a, b bool) { if a && b || a {} }`:                                             4,
		`func f(xs []int) { for range xs {}; for i := 0; i < 1; i++ {} }`:                     3,
		`func f(x int) { switch x { case 1, 2: case 3: default: } }`:                          3,
		`func f(x any) { switch x.(type) { case int: default: } }`:                            2,
		`func f(c chan int) { select { case <-c: default: } }`:                                2,
		`func f() { g := func(a bool) { if a {} }; g(true) }`:                                 2,
		`func f(a, b int) int { if a > b { return a } else if a < b { return b }; return 0 }`: 3,
	}
	for src, want := range cases {
		if got := Complexity(parseFunc(t, src)); got != want {
			t.Errorf("Complexity(%s) = %d, want %d", src, got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.27\n")
	writeFile(t, filepath.Join(root, "a.go"), "package m\n\nfunc A(x bool) int {\n\tif x {\n\t\treturn 1\n\t}\n\treturn 0\n}\n\ntype T[K any] struct{}\n\nfunc (t *T[K]) M() {}\n\nfunc (T[K]) N() {}\n\ntype U[K, V any] struct{}\n\nfunc (U[K, V]) P() {}\n\nfunc decl()\n")
	writeFile(t, filepath.Join(root, "a_test.go"), "package m\n\nfunc TestX() {}\n")
	writeFile(t, filepath.Join(root, "gen.go"), "// Code generated by protoc-gen-go. DO NOT EDIT.\n\npackage m\n\nfunc G() {}\n")
	writeFile(t, filepath.Join(root, "sub", "b.go"), "package sub\n\nfunc B() {}\n")
	writeFile(t, filepath.Join(root, "testdata", "t.go"), "package t\n\nfunc Skip() {}\n")
	writeFile(t, filepath.Join(root, "vendor", "v.go"), "package v\n\nfunc Skip() {}\n")
	writeFile(t, filepath.Join(root, ".hidden", "h.go"), "package h\n\nfunc Skip() {}\n")
	writeFile(t, filepath.Join(root, "README.md"), "not go")
	writeFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/m/nested\n")
	writeFile(t, filepath.Join(root, "nested", "n.go"), "package nested\n\nfunc Skip() {}\n")
	return root
}

func TestScanGo(t *testing.T) {
	funcs, err := ScanGo(testModule(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range funcs {
		names = append(names, f.File+":"+f.Name)
	}
	got := strings.Join(names, " ")
	if got != "a.go:A a.go:T.M a.go:T.N a.go:U.P sub/b.go:B" {
		t.Fatalf("functions = %s", got)
	}
	if funcs[0].CC != 2 || funcs[0].StartLine != 3 || funcs[0].EndLine != 8 {
		t.Errorf("A = %+v", funcs[0])
	}
}

func TestScanGoFromDotRoot(t *testing.T) {
	t.Chdir(testModule(t))
	funcs, err := ScanGo(".")
	if err != nil || len(funcs) != 5 || funcs[0].File != "a.go" {
		t.Fatalf("ScanGo(\".\") = %+v, %v", funcs, err)
	}
}

func TestScanGoErrors(t *testing.T) {
	if _, err := ScanGo(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing dir accepted")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "bad.go"), "package")
	if _, err := ScanGo(root); err == nil {
		t.Error("syntax error accepted")
	}
}

func TestRecvTypeFallback(t *testing.T) {
	if got := recvType(&ast.ArrayType{}); got != "?" {
		t.Errorf("recvType = %q", got)
	}
}

const profile = `mode: set
example.com/m/a.go:1.1,2.2 7 1
example.com/m/a.go:3.21,4.7 1 1
example.com/m/a.go:4.7,6.3 1 0
example.com/m/a.go:4.7,6.3 1 1
example.com/m/a.go:5.1,5.9 4 1
example.com/m/a.go:5.1,5.9 4 0
example.com/m/a.go:7.2,7.10 2 0
example.com/m/a.go:8.2,8.3 2 0
example.com/m/a.go:9.1,9.9 9 1
example.com/m/other.go:1.1,2.2 5 1
`

func TestParseProfileMergesDuplicates(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 8 {
		t.Fatalf("blocks = %+v", blocks)
	}
	for i, want := range map[int]Block{
		2: {File: "example.com/m/a.go", StartLine: 4, NumStmt: 1, Covered: true},
		3: {File: "example.com/m/a.go", StartLine: 5, NumStmt: 4, Covered: true},
	} {
		if blocks[i] != want {
			t.Errorf("merged block %d = %+v, want %+v", i, blocks[i], want)
		}
	}
}

func TestParseProfileErrors(t *testing.T) {
	cases := map[string]string{
		"":                                 "missing mode line",
		"garbage\n":                        "missing mode line",
		"mode: set\na.go:1.1,1.2 1 1\nx\n": "malformed line 3",
	}
	for in, want := range cases {
		if _, err := ParseProfile(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseProfile(%q) err = %v, want %q", in, err, want)
		}
	}
}

func TestGoRows(t *testing.T) {
	funcs := []GoFunc{
		{File: "a.go", Name: "A", CC: 2, StartLine: 3, EndLine: 8},
		{File: "sub/b.go", Name: "B", CC: 1, StartLine: 3, EndLine: 3},
	}
	blocks, _ := ParseProfile(strings.NewReader(profile))
	rows := GoRows(funcs, blocks, "example.com/m")
	// A spans lines 3..8: blocks on lines 1 and 9 are outside it.
	if rows[0] != (Row{"a.go:A", 2, 0.6}) {
		t.Errorf("A = %+v", rows[0])
	}
	if rows[1] != (Row{"sub/b.go:B", 1, 0}) {
		t.Errorf("uncovered B = %+v", rows[1])
	}
}

func TestModulePath(t *testing.T) {
	root := testModule(t)
	if got, err := ModulePath(root); err != nil || got != "example.com/m" {
		t.Errorf("ModulePath = %q, %v", got, err)
	}
	if _, err := ModulePath(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing go.mod: err = %v", err)
	}
	empty := t.TempDir()
	writeFile(t, filepath.Join(empty, "go.mod"), "go 1.27\n")
	if _, err := ModulePath(empty); err == nil {
		t.Error("go.mod without module line accepted")
	}
}
