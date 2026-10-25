// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package vulngate decides whether a vulnerability scan passes (SC1, ADR
// 00XX-draft-supply-chain): a known vulnerability the code calls (govulncheck)
// or a fixable critical or high one in a dependency or image (grype) fails,
// unless an exception with a reason and an expiry date at most 90 days ahead
// names it. An unfixable critical one is a warning.
package vulngate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Severity of a vulnerability, as the scanner rates it.
type Severity int

const (
	Unknown Severity = iota
	Low
	Medium
	High
	Critical
)

var severityNames = map[Severity]string{Unknown: "unknown", Low: "low", Medium: "medium", High: "high", Critical: "critical"}

func (s Severity) String() string { return severityNames[s] }

// ParseSeverity maps a scanner's severity name to a Severity, any case;
// what it does not know (grype's "Negligible", none) is Unknown.
func ParseSeverity(name string) Severity {
	for s, n := range severityNames {
		if strings.EqualFold(name, n) {
			return s
		}
	}
	return Unknown
}

// Finding is one vulnerability in one package.
type Finding struct {
	ID       string
	Aliases  []string
	Package  string
	Version  string
	Severity Severity
	// FixedIn is the first version without the vulnerability; empty when there is none.
	FixedIn string
	// Called means the code calls the vulnerable function (govulncheck).
	Called bool
}

// Blocks reports whether f fails the scan: the code calls it, or it is
// critical or high and a fixed version exists.
func Blocks(f Finding) bool {
	return f.Called || (f.Severity >= High && f.FixedIn != "")
}

// Warns reports whether f is shown as a warning: critical without a fix.
func Warns(f Finding) bool {
	return !Blocks(f) && f.Severity == Critical && f.FixedIn == ""
}

type grypeMatch struct {
	Vulnerability struct {
		ID       string `json:"id"`
		Severity string `json:"severity"`
		Fix      struct {
			State    string   `json:"state"`
			Versions []string `json:"versions"`
		} `json:"fix"`
	} `json:"vulnerability"`
	Related []struct {
		ID string `json:"id"`
	} `json:"relatedVulnerabilities"`
	Artifact struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"artifact"`
}

// ParseGrype reads grype's JSON report (grype -o json). A report without
// "matches" is an error: a scan that did not run must not pass.
func ParseGrype(r io.Reader) ([]Finding, error) {
	var report struct {
		Matches *[]grypeMatch `json:"matches"`
	}
	if err := json.NewDecoder(r).Decode(&report); err != nil {
		return nil, fmt.Errorf("grype report: %w", err)
	}
	if report.Matches == nil {
		return nil, errors.New("grype report: no matches field")
	}
	findings := make([]Finding, 0, len(*report.Matches))
	for _, m := range *report.Matches {
		findings = append(findings, m.finding())
	}
	return findings, nil
}

func (m grypeMatch) finding() Finding {
	f := Finding{
		ID: m.Vulnerability.ID, Package: m.Artifact.Name, Version: m.Artifact.Version,
		Severity: ParseSeverity(m.Vulnerability.Severity),
	}
	if m.Vulnerability.Fix.State == "fixed" && len(m.Vulnerability.Fix.Versions) > 0 {
		f.FixedIn = m.Vulnerability.Fix.Versions[0]
	}
	for _, rel := range m.Related {
		f.Aliases = append(f.Aliases, rel.ID)
	}
	return f
}

type govulncheckMessage struct {
	Config *json.RawMessage `json:"config"`
	OSV    *struct {
		ID      string   `json:"id"`
		Aliases []string `json:"aliases"`
	} `json:"osv"`
	Finding *struct {
		OSV          string `json:"osv"`
		FixedVersion string `json:"fixed_version"`
		Trace        []struct {
			Module   string `json:"module"`
			Version  string `json:"version"`
			Function string `json:"function"`
		} `json:"trace"`
	} `json:"finding"`
}

// ParseGovulncheck reads govulncheck's JSON stream (govulncheck -format json)
// and keeps the vulnerabilities the code calls, once each. A stream without
// its config message is an error: a scan that did not run must not pass.
func ParseGovulncheck(r io.Reader) ([]Finding, error) {
	dec := json.NewDecoder(r)
	s := govulncheckStream{aliases: map[string][]string{}, seen: map[string]bool{}}
	for {
		var msg govulncheckMessage
		err := dec.Decode(&msg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("govulncheck report: %w", err)
		}
		s.add(msg)
	}
	return s.findings()
}

// govulncheckStream collects the messages of one govulncheck run.
type govulncheckStream struct {
	configured bool
	aliases    map[string][]string
	seen       map[string]bool
	called     []Finding
}

func (s *govulncheckStream) add(msg govulncheckMessage) {
	s.configured = s.configured || msg.Config != nil
	if msg.OSV != nil {
		s.aliases[msg.OSV.ID] = msg.OSV.Aliases
	}
	if f, ok := calledFinding(msg); ok && !s.seen[f.ID] {
		s.seen[f.ID] = true
		s.called = append(s.called, f)
	}
}

func (s *govulncheckStream) findings() ([]Finding, error) {
	if !s.configured {
		return nil, errors.New("govulncheck report: no config message")
	}
	for i := range s.called {
		s.called[i].Aliases = s.aliases[s.called[i].ID]
	}
	return s.called, nil
}

// calledFinding is the finding of msg when its trace starts at a function,
// that is, govulncheck found a call to the vulnerable code.
func calledFinding(msg govulncheckMessage) (Finding, bool) {
	if msg.Finding == nil || len(msg.Finding.Trace) == 0 || msg.Finding.Trace[0].Function == "" {
		return Finding{}, false
	}
	top := msg.Finding.Trace[0]
	return Finding{ID: msg.Finding.OSV, Package: top.Module, Version: top.Version, FixedIn: msg.Finding.FixedVersion, Called: true}, true
}

// MaxExceptionDays is how far ahead an exception may expire.
const MaxExceptionDays = 90

// Exception lets a finding pass until a date, for a reason.
type Exception struct {
	ID     string
	Until  time.Time
	Reason string
	Line   int
}

// Problems are the invalid lines of an exceptions file, all of them.
type Problems []string

func (p Problems) Error() string { return strings.Join(p, "\n") }

// ParseExceptions reads the exceptions file: one "<id> <until YYYY-MM-DD>
// <reason>" per line, # starts a comment. An exception without a reason,
// expired before today or expiring more than MaxExceptionDays after it makes
// the whole file invalid.
func ParseExceptions(r io.Reader, today time.Time) ([]Exception, error) {
	var exceptions []Exception
	var problems Problems
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		e, err, ok := exceptionLine(sc.Text(), n, today)
		switch {
		case err != nil:
			problems = append(problems, err.Error())
		case ok:
			exceptions = append(exceptions, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("exceptions: %w", err)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return exceptions, nil
}

// exceptionLine parses line n; ok is false for a blank line or a comment.
func exceptionLine(text string, n int, today time.Time) (e Exception, err error, ok bool) {
	line := strings.TrimSpace(text)
	if line == "" || strings.HasPrefix(line, "#") {
		return Exception{}, nil, false
	}
	e, err = parseException(line, today)
	if err != nil {
		return Exception{}, fmt.Errorf("line %d: %w", n, err), false
	}
	e.Line = n
	return e, nil, true
}

func parseException(line string, today time.Time) (Exception, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Exception{}, errors.New("want <id> <until YYYY-MM-DD> <reason>")
	}
	id := fields[0]
	until, err := time.Parse(time.DateOnly, fields[1])
	if err != nil {
		return Exception{}, fmt.Errorf("%s: until %q is not a date (YYYY-MM-DD)", id, fields[1])
	}
	if len(fields) < 3 {
		return Exception{}, fmt.Errorf("%s: no reason", id)
	}
	if until.Before(today) {
		return Exception{}, fmt.Errorf("%s: expired on %s", id, fields[1])
	}
	if until.After(today.AddDate(0, 0, MaxExceptionDays)) {
		return Exception{}, fmt.Errorf("%s: until %s is more than %d days ahead", id, fields[1], MaxExceptionDays)
	}
	return Exception{ID: id, Until: until, Reason: strings.Join(fields[2:], " ")}, nil
}

// Report sorts the findings of a scan by what they do to it.
type Report struct {
	Blocking []Finding
	Excepted []Finding
	Warnings []Finding
	// Unused are exceptions no finding needs any more.
	Unused []Exception
}

// Failed reports whether the scan fails.
func (r Report) Failed() bool { return len(r.Blocking) > 0 }

// Evaluate applies the threshold and the exceptions to the findings.
func Evaluate(findings []Finding, exceptions []Exception) Report {
	used := map[string]bool{}
	var r Report
	for _, f := range findings {
		r.add(f, exceptedBy(f, exceptions, used))
	}
	for _, e := range exceptions {
		if !used[e.ID] {
			r.Unused = append(r.Unused, e)
		}
	}
	return r
}

func (r *Report) add(f Finding, excepted bool) {
	switch {
	case Blocks(f) && excepted:
		r.Excepted = append(r.Excepted, f)
	case Blocks(f):
		r.Blocking = append(r.Blocking, f)
	case Warns(f):
		r.Warnings = append(r.Warnings, f)
	}
}

// exceptedBy reports whether an exception names f or one of its aliases,
// and marks every such exception used.
func exceptedBy(f Finding, exceptions []Exception, used map[string]bool) bool {
	ids := append([]string{f.ID}, f.Aliases...)
	found := false
	for _, e := range exceptions {
		for _, id := range ids {
			if e.ID == id {
				used[e.ID] = true
				found = true
			}
		}
	}
	return found
}

// Write prints the report, one line per finding and unused exception.
func Write(w io.Writer, r Report) error {
	groups := []struct {
		label    string
		findings []Finding
	}{{"BLOCKING", r.Blocking}, {"EXCEPTED", r.Excepted}, {"WARNING ", r.Warnings}}
	for _, g := range groups {
		for _, f := range g.findings {
			if _, err := fmt.Fprintf(w, "%s %s %s@%s %s\n", g.label, f.ID, f.Package, f.Version, describe(f)); err != nil {
				return err
			}
		}
	}
	for _, e := range r.Unused {
		if _, err := fmt.Fprintf(w, "UNUSED   %s (exceptions line %d): remove it\n", e.ID, e.Line); err != nil {
			return err
		}
	}
	return nil
}

func describe(f Finding) string {
	rating := f.Severity.String()
	if f.Called {
		rating = "called by our code"
	}
	if f.FixedIn == "" {
		return rating + ", no fixed version"
	}
	return rating + ", fixed in " + f.FixedIn
}
