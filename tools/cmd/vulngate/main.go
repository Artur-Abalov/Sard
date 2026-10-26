// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command vulngate fails a vulnerability scan by the threshold of SC1 (ADR
// 00XX-draft-supply-chain): a vulnerability the code calls (govulncheck) or a
// fixable critical or high one (grype) blocks, unless security/vuln-exceptions
// names it with a reason and an expiry date at most 90 days ahead.
//
//	vulngate -format grype|govulncheck -exceptions security/vuln-exceptions [-today 2026-10-25] report.json...
//
// Exit codes: 0 passes, 1 usage, input or exceptions error (an expired
// exception included), 2 blocking findings.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/tools/internal/vulngate"
)

const (
	exitOK      = 0
	exitError   = 1
	exitBlocked = 2
)

type parser func(io.Reader) ([]vulngate.Finding, error)

var parsers = map[string]parser{
	"grype":       vulngate.ParseGrype,
	"govulncheck": vulngate.ParseGovulncheck,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	parse      parser
	exceptions string
	today      time.Time
	reports    []string
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	report, err := evaluate(opts)
	if err != nil {
		return fail(stderr, err)
	}
	if err := vulngate.Write(stdout, report); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stderr, "vulngate: %d blocking, %d excepted, %d warnings\n",
		len(report.Blocking), len(report.Excepted), len(report.Warnings))
	if report.Failed() {
		return exitBlocked
	}
	return exitOK
}

// fail reports err; a failure to write to stderr leaves nothing else to do.
func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "vulngate:", err)
	return exitError
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	var format, today string
	fs := flag.NewFlagSet("vulngate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&format, "format", "", "report format: grype or govulncheck")
	fs.StringVar(&o.exceptions, "exceptions", "", "exceptions file: <id> <until YYYY-MM-DD> <reason> per line")
	fs.StringVar(&today, "today", time.Now().UTC().Format(time.DateOnly), "the date exceptions expire against")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.parse = parsers[format]
	o.reports = fs.Args()
	var err error
	o.today, err = time.Parse(time.DateOnly, today)
	switch {
	case o.parse == nil:
		return o, fmt.Errorf("-format %q: want grype or govulncheck", format)
	case o.exceptions == "":
		return o, errors.New("-exceptions is required")
	case len(o.reports) == 0:
		return o, errors.New("no report given")
	case err != nil:
		return o, fmt.Errorf("-today: %w", err)
	}
	return o, nil
}

func evaluate(o options) (vulngate.Report, error) {
	exceptions, err := readExceptions(o.exceptions, o.today)
	if err != nil {
		return vulngate.Report{}, err
	}
	var findings []vulngate.Finding
	for _, path := range o.reports {
		found, err := readReport(path, o.parse)
		if err != nil {
			return vulngate.Report{}, err
		}
		findings = append(findings, found...)
	}
	return vulngate.Evaluate(findings, exceptions), nil
}

func readExceptions(path string, today time.Time) ([]vulngate.Exception, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	exceptions, err := vulngate.ParseExceptions(f, today)
	if err != nil {
		return nil, fmt.Errorf("%s:\n%w", path, err)
	}
	return exceptions, nil
}

func readReport(path string, parse parser) ([]vulngate.Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	findings, err := parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return findings, nil
}
