// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command crap prints the CRAP metric per function and fails when any
// function exceeds the threshold.
//
//	crap -go-profile cover.out -go-src ./agent [-threshold 6] [-top 10]
//	crap -jacoco build/reports/jacoco/test/jacocoTestReport.xml
//
// Exit codes: 0 within threshold, 1 usage or input error, 2 threshold exceeded.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Artur-Abalov/sard/tools/internal/crap"
)

const (
	exitOK       = 0
	exitError    = 1
	exitExceeded = 2
	worstShown   = 10
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	goProfile, goSrc, jacoco string
	threshold                float64
	top                      int
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	rows, err := load(opts)
	if err != nil {
		return fail(stderr, err)
	}
	crap.Sort(rows)
	offenders := crap.Offenders(rows, opts.threshold)
	top := opts.top
	if top <= 0 {
		top = len(offenders) + worstShown
	}
	if err := crap.Write(stdout, rows, top); err != nil {
		return fail(stderr, err)
	}
	if len(offenders) > 0 {
		_, _ = fmt.Fprintf(stderr, "crap: %d function(s) above threshold %.1f\n", len(offenders), opts.threshold)
		return exitExceeded
	}
	return exitOK
}

// fail reports err; a failure to write to stderr leaves nothing else to do.
func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "crap:", err)
	return exitError
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("crap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.goProfile, "go-profile", "", "Go coverage profile (go test -coverprofile)")
	fs.StringVar(&o.goSrc, "go-src", "", "Go module root the profile was produced for")
	fs.StringVar(&o.jacoco, "jacoco", "", "JaCoCo XML report")
	fs.Float64Var(&o.threshold, "threshold", 6, "maximum allowed CRAP per function")
	fs.IntVar(&o.top, "top", 0, "rows to print (default: offenders plus the 10 worst)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	return o, validate(o)
}

var (
	errNoInput = errors.New("no input: give -jacoco or -go-profile with -go-src")
	errBoth    = errors.New("use either -jacoco or -go-profile/-go-src, not both")
	errPair    = errors.New("-go-profile and -go-src must be given together")

	// inputErrors maps the set of given inputs (j = jacoco, p = profile,
	// s = source) to the error it produces; valid sets are absent.
	inputErrors = map[string]error{
		"": errNoInput, "p": errPair, "s": errPair,
		"jp": errBoth, "js": errBoth, "jps": errBoth,
	}
)

func validate(o options) error {
	return inputErrors[inputKind(o)]
}

func inputKind(o options) string {
	k := ""
	if o.jacoco != "" {
		k += "j"
	}
	if o.goProfile != "" {
		k += "p"
	}
	if o.goSrc != "" {
		k += "s"
	}
	return k
}

func load(o options) ([]crap.Row, error) {
	if o.jacoco != "" {
		return loadJaCoCo(o.jacoco)
	}
	return loadGo(o.goProfile, o.goSrc)
}

func loadJaCoCo(path string) ([]crap.Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return crap.ParseJaCoCo(bytes.NewReader(data))
}

func loadGo(profile, src string) ([]crap.Row, error) {
	module, err := crap.ModulePath(src)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		return nil, err
	}
	blocks, err := crap.ParseProfile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	funcs, err := crap.ScanGo(src)
	if err != nil {
		return nil, err
	}
	return crap.GoRows(funcs, blocks, module), nil
}
