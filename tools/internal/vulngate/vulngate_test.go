// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package vulngate

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"Critical": Critical, "critical": Critical, "HIGH": High, "Medium": Medium,
		"Low": Low, "Negligible": Unknown, "": Unknown, "bogus": Unknown,
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{Critical: "critical", High: "high", Medium: "medium", Low: "low", Unknown: "unknown"}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}

const grypeFixture = `{"matches":[
 {"vulnerability":{"id":"CVE-1","severity":"Critical","fix":{"state":"fixed","versions":["1.2.4","1.3.0"]}},
  "relatedVulnerabilities":[{"id":"GHSA-aaaa"}],"artifact":{"name":"lib-a","version":"1.2.3"}},
 {"vulnerability":{"id":"CVE-2","severity":"High","fix":{"state":"not-fixed","versions":[]}},
  "artifact":{"name":"lib-b","version":"2.0"}},
 {"vulnerability":{"id":"GHSA-cccc","severity":"Medium","fix":{"state":"fixed","versions":["3.1"]}},
  "relatedVulnerabilities":[{"id":"CVE-3"},{"id":"CVE-3b"}],"artifact":{"name":"lib-c","version":"3.0"}}
]}`

func TestParseGrype(t *testing.T) {
	got, err := ParseGrype(strings.NewReader(grypeFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{
		{ID: "CVE-1", Aliases: []string{"GHSA-aaaa"}, Package: "lib-a", Version: "1.2.3", Severity: Critical, FixedIn: "1.2.4"},
		{ID: "CVE-2", Package: "lib-b", Version: "2.0", Severity: High},
		{ID: "GHSA-cccc", Aliases: []string{"CVE-3", "CVE-3b"}, Package: "lib-c", Version: "3.0", Severity: Medium, FixedIn: "3.1"},
	}
	assertFindings(t, got, want)
}

func TestParseGrypeEmptyAndBroken(t *testing.T) {
	got, err := ParseGrype(strings.NewReader(`{"matches":[]}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("empty report = %v, %v", got, err)
	}
	if _, err := ParseGrype(strings.NewReader(`{"matches":`)); err == nil {
		t.Fatal("broken report accepted")
	}
	if _, err := ParseGrype(strings.NewReader(`{}`)); err == nil {
		t.Fatal("a report without matches accepted: a scan that did not run must not pass")
	}
}

const govulncheckFixture = `
{"config":{"protocol_version":"v1.0.0"}}
{"progress":{"message":"Scanning your code"}}
{"osv":{"id":"GO-2026-0001","aliases":["CVE-2026-1","GHSA-x"]}}
{"osv":{"id":"GO-2026-0002","aliases":[]}}
{"finding":{"osv":"GO-2026-0001","fixed_version":"v1.2.4","trace":[{"module":"example.com/m","version":"v1.2.3","package":"example.com/m/p"}]}}
{"finding":{"osv":"GO-2026-0001","fixed_version":"v1.2.4","trace":[{"module":"example.com/m","version":"v1.2.3","package":"example.com/m/p","function":"Parse"},{"module":"sard","function":"main"}]}}
{"finding":{"osv":"GO-2026-0001","fixed_version":"v1.2.4","trace":[{"module":"example.com/m","version":"v1.2.3","package":"example.com/m/p","function":"Other"}]}}
{"finding":{"osv":"GO-2026-0002","trace":[{"module":"stdlib","version":"v1.27.1","package":"net/http","function":"Serve"}]}}
{"finding":{"osv":"GO-2026-0003","fixed_version":"v0.2.0","trace":[{"module":"example.com/n","version":"v0.1.0"}]}}
`

func TestParseGovulncheckKeepsCalledFindingsOnce(t *testing.T) {
	got, err := ParseGovulncheck(strings.NewReader(govulncheckFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{
		{ID: "GO-2026-0001", Aliases: []string{"CVE-2026-1", "GHSA-x"}, Package: "example.com/m", Version: "v1.2.3", FixedIn: "v1.2.4", Called: true},
		{ID: "GO-2026-0002", Package: "stdlib", Version: "v1.27.1", Called: true},
	}
	assertFindings(t, got, want)
}

func TestParseGovulncheckBroken(t *testing.T) {
	if _, err := ParseGovulncheck(strings.NewReader(`{"finding":`)); err == nil {
		t.Fatal("broken report accepted")
	}
	if _, err := ParseGovulncheck(strings.NewReader(``)); err == nil {
		t.Fatal("an empty report accepted: a scan that did not run must not pass")
	}
	got, err := ParseGovulncheck(strings.NewReader(`{"config":{"protocol_version":"v1.0.0"}}`))
	if err != nil || len(got) != 0 {
		t.Fatalf("a clean report = %v, %v", got, err)
	}
}

func TestBlocks(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want bool
	}{
		{"fixable critical", Finding{Severity: Critical, FixedIn: "1"}, true},
		{"fixable high", Finding{Severity: High, FixedIn: "1"}, true},
		{"fixable medium", Finding{Severity: Medium, FixedIn: "1"}, false},
		{"unfixed critical", Finding{Severity: Critical}, false},
		{"called by our code, no severity, no fix", Finding{Called: true}, true},
		{"unknown severity", Finding{FixedIn: "1"}, false},
	}
	for _, c := range cases {
		if got := Blocks(c.f); got != c.want {
			t.Errorf("%s: Blocks = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWarns(t *testing.T) {
	if !Warns(Finding{Severity: Critical}) {
		t.Error("an unfixed critical must warn")
	}
	for _, f := range []Finding{{Severity: Critical, FixedIn: "1"}, {Severity: High}, {Called: true, Severity: Critical}} {
		if Warns(f) {
			t.Errorf("%+v warns", f)
		}
	}
}

func TestParseExceptions(t *testing.T) {
	file := `# id   until   reason
CVE-1  2026-12-01  not reachable: the parser is never given untrusted input

  GHSA-x 2026-11-01   the fixed release breaks the API; upgrade in Q1
`
	got, err := ParseExceptions(strings.NewReader(file), day(t, "2026-10-25"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Exception{
		{ID: "CVE-1", Until: day(t, "2026-12-01"), Reason: "not reachable: the parser is never given untrusted input", Line: 2},
		{ID: "GHSA-x", Until: day(t, "2026-11-01"), Reason: "the fixed release breaks the API; upgrade in Q1", Line: 4},
	}
	if g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want); g != w {
		t.Errorf("exceptions:\n%s\nwant:\n%s", g, w)
	}
}

func TestParseExceptionsRefuses(t *testing.T) {
	today := day(t, "2026-10-25")
	cases := map[string]string{
		"CVE-1 2026-12-01":         "line 1: CVE-1: no reason",
		"CVE-1 2026-12-01   ":      "line 1: CVE-1: no reason",
		"CVE-1":                    "line 1: want <id> <until YYYY-MM-DD> <reason>",
		"CVE-1 tomorrow because":   `line 1: CVE-1: until "tomorrow" is not a date (YYYY-MM-DD)`,
		"CVE-1 2026-10-24 because": "line 1: CVE-1: expired on 2026-10-24",
		"CVE-1 2027-01-24 because": "line 1: CVE-1: until 2027-01-24 is more than 90 days ahead",
	}
	for line, want := range cases {
		_, err := ParseExceptions(strings.NewReader(line), today)
		if err == nil || err.Error() != want {
			t.Errorf("%q: error %v, want %q", line, err, want)
		}
	}
}

func TestParseExceptionsLimits(t *testing.T) {
	today := day(t, "2026-10-25")
	for _, line := range []string{"CVE-1 2026-10-25 last day today", "CVE-1 2027-01-23 exactly 90 days"} {
		if _, err := ParseExceptions(strings.NewReader(line), today); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestParseExceptionsListsEveryProblem(t *testing.T) {
	_, err := ParseExceptions(strings.NewReader("A 2026-12-01\nB 2026-12-01 ok\nC 2026-01-01 old"), day(t, "2026-10-25"))
	var problems Problems
	if !errors.As(err, &problems) || len(problems) != 2 {
		t.Fatalf("error = %v", err)
	}
	if err.Error() != "line 1: A: no reason\nline 3: C: expired on 2026-01-01" {
		t.Errorf("message = %q", err.Error())
	}
}

func TestParseExceptionsReadError(t *testing.T) {
	if _, err := ParseExceptions(failingReader{}, day(t, "2026-10-25")); err == nil {
		t.Fatal("read error swallowed")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestEvaluate(t *testing.T) {
	findings := []Finding{
		{ID: "CVE-1", Severity: Critical, FixedIn: "2"},
		{ID: "GHSA-2", Aliases: []string{"CVE-2"}, Severity: High, FixedIn: "2"},
		{ID: "CVE-3", Severity: Critical},
		{ID: "CVE-4", Severity: Low, FixedIn: "2"},
		{ID: "GO-5", Called: true},
	}
	exceptions := []Exception{{ID: "CVE-2", Reason: "r"}, {ID: "CVE-9", Reason: "stale", Line: 7}}
	r := Evaluate(findings, exceptions)
	assertIDs(t, "blocking", r.Blocking, "CVE-1", "GO-5")
	assertIDs(t, "excepted", r.Excepted, "GHSA-2")
	assertIDs(t, "warnings", r.Warnings, "CVE-3")
	if len(r.Unused) != 1 || r.Unused[0].ID != "CVE-9" {
		t.Errorf("unused = %+v", r.Unused)
	}
	if !r.Failed() {
		t.Error("blocking findings do not fail")
	}
	if (Report{Excepted: r.Excepted, Warnings: r.Warnings, Unused: r.Unused}).Failed() {
		t.Error("a report without blocking findings fails")
	}
}

func TestEvaluateMatchesTheOwnID(t *testing.T) {
	r := Evaluate([]Finding{{ID: "CVE-1", Severity: High, FixedIn: "2"}}, []Exception{{ID: "CVE-1", Reason: "r"}})
	if len(r.Blocking) != 0 || len(r.Excepted) != 1 || len(r.Unused) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestEvaluateUsedByAnyFinding(t *testing.T) {
	r := Evaluate([]Finding{{ID: "CVE-1", Severity: Low}, {ID: "CVE-2"}}, []Exception{{ID: "CVE-2", Reason: "r"}})
	if len(r.Unused) != 0 {
		t.Fatalf("an exception that matches a finding below the threshold is unused: %+v", r.Unused)
	}
}

func TestWrite(t *testing.T) {
	r := Report{
		Blocking: []Finding{{ID: "CVE-1", Package: "lib", Version: "1.0", Severity: High, FixedIn: "1.1"}, {ID: "GO-2", Package: "stdlib", Version: "v1.27.1", Called: true}},
		Excepted: []Finding{{ID: "CVE-3", Package: "x", Version: "2"}},
		Warnings: []Finding{{ID: "CVE-4", Package: "y", Version: "3", Severity: Critical}},
		Unused:   []Exception{{ID: "CVE-9", Line: 4}},
	}
	var b strings.Builder
	if err := Write(&b, r); err != nil {
		t.Fatal(err)
	}
	want := `BLOCKING CVE-1 lib@1.0 high, fixed in 1.1
BLOCKING GO-2 stdlib@v1.27.1 called by our code, no fixed version
EXCEPTED CVE-3 x@2 unknown, no fixed version
WARNING  CVE-4 y@3 critical, no fixed version
UNUSED   CVE-9 (exceptions line 4): remove it
`
	if b.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestWriteError(t *testing.T) {
	if err := Write(failingWriter{}, Report{Blocking: []Finding{{ID: "CVE-1"}}}); err == nil {
		t.Fatal("write error swallowed")
	}
	if err := Write(failingWriter{}, Report{Unused: []Exception{{ID: "CVE-1"}}}); err == nil {
		t.Fatal("write error of an unused exception swallowed")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func assertFindings(t *testing.T, got, want []Finding) {
	t.Helper()
	if g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want); g != w {
		t.Errorf("findings:\n%s\nwant:\n%s", g, w)
	}
}

func assertIDs(t *testing.T, what string, got []Finding, want ...string) {
	t.Helper()
	ids := make([]string, len(got))
	for i, f := range got {
		ids[i] = f.ID
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, ids, want)
	}
}
