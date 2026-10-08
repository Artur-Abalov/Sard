// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
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
	// offerRemote: the flags of an s3: address and --connect-timeout (A8b).
	offerRemote
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
	// The flags of an s3: address (Р29): the secret key comes from the
	// standard input, a file or the terminal, never from a value.
	accessKeyID, region string
	secretKeyStdin      bool
	secretKeyFromFile   string
	// connectTimeout bounds the first access to the storage (Р33).
	connectTimeout time.Duration
	// set are the flags the command line gave, even with an empty value.
	set map[string]bool
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
	"add":      {"repo add", offerTimeout | offerNoRestart | offerPasswordSource | offerRemote, 2, "a name and an address are required: sard-agent repo add [flags] <name> <address>"},
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
	accessKeyID, region          *string
	secretKeyFromFile            *string
	secretKeyStdin               *bool
	connectTimeout               *time.Duration
}

func defineFlags(fs *flag.FlagSet, o offer, defaultConfig string) flagValues {
	v := flagValues{
		config: fs.String("config", defaultConfig, ""), timeout: new(time.Duration),
		generate: new(bool), json: new(bool), noRestart: new(bool), reveal: new(bool),
		stdin: new(bool), passwordStdin: new(bool), fromFile: new(string), passwordFromFile: new(string),
		accessKeyID: new(string), region: new(string), secretKeyFromFile: new(string),
		secretKeyStdin: new(bool), connectTimeout: new(time.Duration),
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
	if o&offerRemote != 0 {
		defineRemoteFlags(fs, &v)
	}
	return v
}

// defineRemoteFlags are the flags of an s3: address and --connect-timeout.
func defineRemoteFlags(fs *flag.FlagSet, v *flagValues) {
	v.accessKeyID = fs.String("access-key-id", "", "")
	v.region = fs.String("region", "", "")
	v.secretKeyStdin = fs.Bool("secret-key-stdin", false, "")
	v.secretKeyFromFile = fs.String("secret-key-from-file", "", "")
	*v.connectTimeout = defaultConnectTimeout
	fs.Var(positiveDuration{v.connectTimeout}, "connect-timeout", "")
}

// defaultConnectTimeout bounds the first access to the storage unless
// --connect-timeout says otherwise (Р33).
const defaultConnectTimeout = 30 * time.Second

// positiveDuration is a duration flag that refuses zero and less, naming itself.
type positiveDuration struct{ d *time.Duration }

func (p positiveDuration) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return errors.New("--connect-timeout must be a positive duration")
	}
	*p.d = d
	return nil
}

func (p positiveDuration) String() string {
	if p.d == nil {
		return ""
	}
	return p.d.String()
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
		accessKeyID: *v.accessKeyID, region: *v.region, secretKeyStdin: *v.secretKeyStdin,
		secretKeyFromFile: *v.secretKeyFromFile, connectTimeout: *v.connectTimeout, set: map[string]bool{},
	}
	fs.Visit(func(f *flag.Flag) { opts.set[f.Name] = true })
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
