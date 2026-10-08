// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"time"
)

// offer is the set of flags a command accepts besides --config.
type offer uint

const (
	offerTimeout offer = 1 << iota
	offerJSON
	offerNoRestart
	offerReveal
	offerGenerate
	offerStdin
	offerFromFile
	offerPasswordSource
)

// hostOptions are the flags and the positional arguments of one command of
// "sard-agent repo ..." or "sard-agent secret ...".
type hostOptions struct {
	configPath string
	timeout    time.Duration
	generate   bool
	json       bool
	noRestart  bool
	reveal     bool
	// stdin and fromFile are --stdin and --from-file of secret set;
	// passwordStdin and passwordFromFile are the flags of repo add.
	stdin            bool
	fromFile         string
	passwordStdin    bool
	passwordFromFile string
	// name and address are the first and the second positional argument.
	name, address string
	// args are the arguments as typed, for the sudo hint.
	args []string
}

// cmdSpec describes the command line of one command.
type cmdSpec struct {
	// words is the command, "repo init".
	words string
	offer offer
	// positional is how many names the command takes.
	positional int
	// missing is the message for too few of them.
	missing string
}

// repoSpecs are the commands of "sard-agent repo".
var repoSpecs = map[string]cmdSpec{
	"init":     {"repo init", offerTimeout | offerGenerate, 1, "the name of a repository of the agent config is required: sard-agent repo init [flags] <name>"},
	"list":     {"repo list", offerTimeout | offerJSON, 0, ""},
	"add":      {"repo add", offerTimeout | offerNoRestart | offerPasswordSource, 2, "a name and an address are required: sard-agent repo add [flags] <name> <address>"},
	"show":     {"repo show", offerTimeout | offerJSON, 1, "the name of a repository is required: sard-agent repo show [flags] <name>"},
	"remove":   {"repo remove", offerNoRestart, 1, "the name of a repository is required: sard-agent repo remove [flags] <name>"},
	"password": {"repo password", offerReveal, 1, "the name of a repository is required: sard-agent repo password <name> --reveal"},
}

// secretSpecs are the commands of "sard-agent secret".
var secretSpecs = map[string]cmdSpec{
	"set":    {"secret set", offerNoRestart | offerStdin | offerFromFile, 1, "the name of a secret is required: sard-agent secret set [flags] <name>"},
	"list":   {"secret list", offerJSON, 0, ""},
	"remove": {"secret remove", offerNoRestart, 1, "the name of a secret is required: sard-agent secret remove [flags] <name>"},
}

// parseRepoFlags reads the flags of a command of "sard-agent repo". Flags
// may stand before and after the names (С1). No flag takes a password or
// a key (ADR 0008); a bad flag is never echoed with its value.
func parseRepoFlags(sub string, args []string, stderr io.Writer, deps hostDeps) (hostOptions, int) {
	return parseFlags(repoSpecs[sub], args, stderr, deps.defaultConfig)
}

func parseSecretFlags(sub string, args []string, stderr io.Writer, deps hostDeps) (hostOptions, int) {
	return parseFlags(secretSpecs[sub], args, stderr, deps.defaultConfig)
}

// flagValues are the pointers a command's flags are parsed into.
type flagValues struct {
	config                       *string
	timeout                      *time.Duration
	generate, json, noRestart    *bool
	reveal, stdin, passwordStdin *bool
	fromFile, passwordFromFile   *string
}

func defineFlags(fs *flag.FlagSet, o offer, defaultConfig string) flagValues {
	v := flagValues{
		config: fs.String("config", defaultConfig, ""), timeout: new(time.Duration),
		generate: new(bool), json: new(bool), noRestart: new(bool), reveal: new(bool),
		stdin: new(bool), passwordStdin: new(bool), fromFile: new(string), passwordFromFile: new(string),
	}
	if o&offerTimeout != 0 {
		v.timeout = fs.Duration("timeout", defaultRepoTimeout, "")
	}
	boolFlag := func(bit offer, p **bool, name string) {
		if o&bit != 0 {
			*p = fs.Bool(name, false, "")
		}
	}
	boolFlag(offerJSON, &v.json, "json")
	boolFlag(offerNoRestart, &v.noRestart, "no-restart")
	boolFlag(offerReveal, &v.reveal, "reveal")
	boolFlag(offerGenerate, &v.generate, "generate-password")
	boolFlag(offerStdin, &v.stdin, "stdin")
	if o&offerFromFile != 0 {
		v.fromFile = fs.String("from-file", "", "")
	}
	if o&offerPasswordSource != 0 {
		v.passwordStdin = fs.Bool("password-stdin", false, "")
		v.passwordFromFile = fs.String("password-from-file", "", "")
	}
	return v
}

func parseFlags(spec cmdSpec, args []string, stderr io.Writer, defaultConfig string) (hostOptions, int) {
	fs := flag.NewFlagSet("sard-agent "+spec.words, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	v := defineFlags(fs, spec.offer, defaultConfig)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return hostOptions{}, usageFailure(stderr, spec.words, scrubFlagError(err))
	}
	if msg := checkPositional(spec, positional); msg != "" {
		return hostOptions{}, usageFailure(stderr, spec.words, msg)
	}
	if spec.offer&offerTimeout != 0 && *v.timeout <= 0 {
		return hostOptions{}, usageFailure(stderr, spec.words, fmt.Sprintf("--timeout must be a positive duration, got %q", v.timeout.String()))
	}
	opts := hostOptions{
		configPath: *v.config, timeout: *v.timeout, generate: *v.generate, json: *v.json,
		noRestart: *v.noRestart, reveal: *v.reveal, stdin: *v.stdin, fromFile: *v.fromFile,
		passwordStdin: *v.passwordStdin, passwordFromFile: *v.passwordFromFile, args: args,
	}
	setNames(&opts, positional)
	return opts, exitOK
}

func setNames(opts *hostOptions, positional []string) {
	if len(positional) > 0 {
		opts.name = positional[0]
	}
	if len(positional) > 1 {
		opts.address = positional[1]
	}
}

// checkPositional is the message for a wrong number of names.
func checkPositional(spec cmdSpec, positional []string) string {
	switch {
	case len(positional) > spec.positional:
		return "unexpected extra argument"
	case len(positional) < spec.positional:
		return spec.missing
	}
	return ""
}

var invalidValue = regexp.MustCompile(`invalid value "[^"]*" for flag`)

// scrubFlagError is the flag package's message without the value it
// rejected: a secret typed in the wrong place must not be echoed.
func scrubFlagError(err error) string {
	return invalidValue.ReplaceAllString(err.Error(), "invalid value for flag")
}

func usageFailure(stderr io.Writer, words, msg string) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent %s: %s\n", words, msg)
	return exitUsage
}

// parseInterspersed parses fs.Parse's flags before and after the
// positional arguments and returns the latter.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if args = fs.Args(); len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}
