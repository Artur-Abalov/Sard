// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restore"
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
	// offerRemote: the flags of an s3: address and an sftp: one (A8b).
	offerRemote
	// offerConnectTimeout: --connect-timeout (Р33).
	offerConnectTimeout
	// offerSource: --source of repo snapshots (Р57).
	offerSource
	// offerRestore: --target, --overwrite, --allow-root-target, --path and a
	// --timeout that is not set unless the operator gives it (Р59, Р61, Р64).
	offerRestore
	// offerShortConnect: --connect-timeout is 10s by default, not 30s (A8d,
	// ЧВ9: a check is a matter of seconds).
	offerShortConnect
	// offerReadData: --read-data <percent> of repo check (Ч7).
	offerReadData
	// offerRemoveStale: --remove-stale, which repo unlock needs (Ч6).
	offerRemoveStale
	// offerOffline: --offline of check (Ч22); offerNoReadData: --read-data is
	// refused with the name of the command that has it (A8d-2).
	offerOffline
	offerNoReadData
	// offerKeyAdd: --for, --label, --to-file and --if-missing of repo key add (V3).
	offerKeyAdd
	// offerYes: --yes of repo key remove.
	offerYes
	// offerRecoveryKey: --recovery-key-file and --no-recovery-key of repo add and repo init (K15).
	offerRecoveryKey
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
	// provider is the preset of the storage address (Р50).
	provider          string
	secretKeyStdin    bool
	secretKeyFromFile string
	// connectTimeout bounds the first access to the storage (Р33).
	connectTimeout time.Duration
	// source is --source: the id of a source of Sard (Р57).
	source string
	// readData is --read-data: the percent of the data repo check reads, 0 if
	// not given; removeStale is --remove-stale of repo unlock (A8d).
	readData    int
	removeStale bool
	// offline is --offline of check: no network, no restic on a repository.
	offline bool
	// The flags of repo restore (Р59, Р61): the directory to restore into,
	// the permission to replace files in it and to restore into the root of
	// the file system, and the paths of the snapshot to restore.
	target               string
	overwrite, allowRoot bool
	paths                []string
	// The flags of an sftp: address (Р40): the fingerprint that confirms the
	// host key, and the permission to replace a host key that changed.
	hostKeyFingerprint string
	replaceHostKey     bool
	// The flags of repo key add: what the key is for, its label, the file its
	// password goes to, and whether an existing key is no refusal.
	purpose, label, toFile string
	ifMissing              bool
	// yes is --yes of repo key remove.
	yes bool
	// The flags of repo add and repo init (K15): the file the password of the
	// recovery key goes to, and the refusal to issue one.
	recoveryKeyFile string
	noRecoveryKey   bool
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
	// optional is how many more names it takes, beyond positional.
	optional int
}

// repoSpecs are the commands of "sard-agent repo".
var repoSpecs = map[string]cmdSpec{
	"init":       {"repo init", offerTimeout | offerGenerate | offerRecoveryKey, 1, "the name of a repository of the agent config is required: sard-agent repo init [flags] <name>", 0},
	"list":       {"repo list", offerTimeout | offerJSON, 0, "", 0},
	"add":        {"repo add", offerTimeout | offerNoRestart | offerPasswordSource | offerRemote | offerRecoveryKey, 2, "a name and an address are required: sard-agent repo add [flags] <name> <address>", 0},
	"show":       {"repo show", offerTimeout | offerJSON, 1, "the name of a repository is required: sard-agent repo show [flags] <name>", 0},
	"remove":     {"repo remove", offerNoRestart, 1, "the name of a repository is required: sard-agent repo remove [flags] <name>", 0},
	"password":   {"repo password", offerReveal, 1, "the name of a repository is required: sard-agent repo password <name> --reveal", 0},
	"snapshots":  {"repo snapshots", offerTimeout | offerJSON | offerConnectTimeout | offerSource, 1, "the name of a repository is required: sard-agent repo snapshots [flags] <name>", 0},
	"restore":    {"repo restore", offerRestore | offerConnectTimeout | offerSource, 2, "a repository name and a snapshot are required: sard-agent repo restore [flags] <name> <snapshot> --target <dir>", 0},
	"check":      {"repo check", offerTimeout | offerJSON | offerConnectTimeout | offerShortConnect | offerReadData, 0, "", 1},
	"key add":    {"repo key add", offerTimeout | offerNoRestart | offerConnectTimeout | offerKeyAdd, 1, "the name of a repository is required: sard-agent repo key add [flags] <name> --for verifier|recovery|other", 0},
	"key list":   {"repo key list", offerTimeout | offerJSON | offerConnectTimeout, 1, "the name of a repository is required: sard-agent repo key list [flags] <name>", 0},
	"key remove": {"repo key remove", offerTimeout | offerNoRestart | offerConnectTimeout | offerYes, 2, "a repository name and a key id are required: sard-agent repo key remove [flags] <name> <id>", 0},
	"unlock":     {"repo unlock", offerTimeout | offerConnectTimeout | offerRemoveStale, 1, "the name of a repository is required: sard-agent repo unlock <name> --remove-stale", 0},
}

// checkSpec is sard-agent check (A8d-2).
var checkSpec = cmdSpec{"check", offerJSON | offerConnectTimeout | offerShortConnect | offerOffline | offerNoReadData, 0, "", 0}

// secretSpecs are the commands of "sard-agent secret".
var secretSpecs = map[string]cmdSpec{
	"set":    {"secret set", offerNoRestart | offerStdin | offerFromFile, 1, "the name of a secret is required: sard-agent secret set [flags] <name>", 0},
	"list":   {"secret list", offerJSON, 0, "", 0},
	"remove": {"secret remove", offerNoRestart, 1, "the name of a secret is required: sard-agent secret remove [flags] <name>", 0},
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
	provider                     *string
	secretKeyFromFile            *string
	secretKeyStdin               *bool
	connectTimeout               *time.Duration
	source                       *string
	hostKeyFingerprint           *string
	replaceHostKey               *bool
	readData                     *int
	removeStale, offline         *bool
	target                       *string
	overwrite, allowRoot         *bool
	paths                        *stringList
	purpose, label, toFile       *string
	ifMissing, yes, noRecovery   *bool
	recoveryKeyFile              *string
}

func defineFlags(fs *flag.FlagSet, o offer, defaultConfig string) flagValues {
	v := flagValues{
		config: fs.String("config", defaultConfig, ""), timeout: new(time.Duration),
		generate: new(bool), json: new(bool), noRestart: new(bool), reveal: new(bool),
		stdin: new(bool), passwordStdin: new(bool), fromFile: new(string), passwordFromFile: new(string),
		accessKeyID: new(string), region: new(string), provider: new(string), secretKeyFromFile: new(string),
		secretKeyStdin: new(bool), connectTimeout: new(time.Duration), source: new(string),
		hostKeyFingerprint: new(string), replaceHostKey: new(bool),
		target: new(string), overwrite: new(bool), allowRoot: new(bool), paths: new(stringList),
		readData: new(int), removeStale: new(bool), offline: new(bool),
		purpose: new(string), label: new(string), toFile: new(string), recoveryKeyFile: new(string),
		ifMissing: new(bool), yes: new(bool), noRecovery: new(bool),
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
	defineReadFlags(fs, o, &v)
	defineKeyFlags(fs, o, &v)
	return v
}

// defineKeyFlags are the flags of the restic key commands and of the recovery
// key that repo add and repo init issue (V3).
func defineKeyFlags(fs *flag.FlagSet, o offer, v *flagValues) {
	if o&offerKeyAdd != 0 {
		v.purpose = fs.String("for", "", "")
		v.label = fs.String("label", "", "")
		v.toFile = fs.String("to-file", "", "")
		v.ifMissing = fs.Bool("if-missing", false, "")
	}
	if o&offerYes != 0 {
		v.yes = fs.Bool("yes", false, "")
	}
	if o&offerRecoveryKey != 0 {
		v.recoveryKeyFile = fs.String("recovery-key-file", "", "")
		v.noRecovery = fs.Bool("no-recovery-key", false, "")
	}
}

// defineReadFlags are --connect-timeout, which the commands that reach a
// remote storage have, and --source of repo snapshots.
func defineReadFlags(fs *flag.FlagSet, o offer, v *flagValues) {
	if o&(offerRemote|offerConnectTimeout) != 0 {
		*v.connectTimeout = defaultConnectTimeout
		if o&offerShortConnect != 0 {
			*v.connectTimeout = shortConnectTimeout
		}
		fs.Var(positiveDuration{name: "connect-timeout", d: v.connectTimeout}, "connect-timeout", "")
	}
	defineHostCheckFlags(fs, o, v)
	if o&offerSource != 0 {
		v.source = fs.String("source", "", "")
	}
	if o&offerRestore != 0 {
		defineRestoreFlags(fs, v)
	}
}

// defineRestoreFlags are the flags of repo restore. --timeout has no
// default: a restore takes as long as it takes (Р64).
func defineRestoreFlags(fs *flag.FlagSet, v *flagValues) {
	v.target = fs.String("target", "", "")
	v.overwrite = fs.Bool("overwrite", false, "")
	v.allowRoot = fs.Bool("allow-root-target", false, "")
	fs.Var(v.paths, "path", "")
	fs.Var(positiveDuration{name: "timeout", d: v.timeout}, "timeout", "")
}

// stringList is a flag that may be given many times.
type stringList []string

func (l *stringList) Set(s string) error {
	*l = append(*l, s)
	return nil
}

func (l *stringList) String() string {
	if l == nil {
		return ""
	}
	return strings.Join(*l, ",")
}

// defineRemoteFlags are the flags of an s3: or an sftp: address.
func defineRemoteFlags(fs *flag.FlagSet, v *flagValues) {
	v.accessKeyID = fs.String("access-key-id", "", "")
	v.region = fs.String("region", "", "")
	v.provider = fs.String("provider", "", "")
	v.secretKeyStdin = fs.Bool("secret-key-stdin", false, "")
	v.secretKeyFromFile = fs.String("secret-key-from-file", "", "")
	v.hostKeyFingerprint = fs.String("host-key-fingerprint", "", "")
	v.replaceHostKey = fs.Bool("replace-host-key", false, "")
}

// defaultConnectTimeout bounds the first access to the storage unless
// --connect-timeout says otherwise (Р33).
const defaultConnectTimeout = 30 * time.Second

// defineHostCheckFlags are --read-data of repo check and --remove-stale of
// repo unlock (A8d).
func defineHostCheckFlags(fs *flag.FlagSet, o offer, v *flagValues) {
	if o&offerReadData != 0 {
		fs.Var(percentFlag{d: v.readData}, "read-data", "")
	}
	if o&offerRemoveStale != 0 {
		v.removeStale = fs.Bool("remove-stale", false, "")
	}
	if o&offerOffline != 0 {
		v.offline = fs.Bool("offline", false, "")
	}
	if o&offerNoReadData != 0 {
		fs.Var(refusedFlag{"--read-data is a flag of `sard-agent repo check`: check reads no data of the repositories"}, "read-data", "")
	}
}

// refusedFlag is a flag that is known only to say that the command does not
// take it.
type refusedFlag struct{ message string }

func (r refusedFlag) Set(string) error { return errors.New(r.message) }
func (r refusedFlag) String() string   { return "" }

// shortConnectTimeout is --connect-timeout of the checks (A8d, ЧВ9).
const shortConnectTimeout = 10 * time.Second

// percentFlag is --read-data: a whole number from 1 to 100.
type percentFlag struct{ d *int }

func (p percentFlag) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 100 {
		return errors.New("--read-data must be a whole number from 1 to 100")
	}
	*p.d = n
	return nil
}

func (p percentFlag) String() string {
	if p.d == nil || *p.d == 0 {
		return ""
	}
	return strconv.Itoa(*p.d)
}

// positiveDuration is a duration flag that refuses zero and less, naming itself.
type positiveDuration struct {
	name string
	d    *time.Duration
}

func (p positiveDuration) Set(s string) error {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return fmt.Errorf("--%s must be a positive duration", p.name)
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
		accessKeyID: *v.accessKeyID, region: *v.region, provider: *v.provider, secretKeyStdin: *v.secretKeyStdin,
		secretKeyFromFile: *v.secretKeyFromFile, connectTimeout: *v.connectTimeout, source: *v.source, set: map[string]bool{},
		hostKeyFingerprint: *v.hostKeyFingerprint, replaceHostKey: *v.replaceHostKey,
		target: *v.target, overwrite: *v.overwrite, allowRoot: *v.allowRoot, paths: *v.paths,
		readData: *v.readData, removeStale: *v.removeStale, offline: *v.offline,
		purpose: *v.purpose, label: *v.label, toFile: *v.toFile, ifMissing: *v.ifMissing, yes: *v.yes,
		recoveryKeyFile: *v.recoveryKeyFile, noRecoveryKey: *v.noRecovery,
	}
	fs.Visit(func(f *flag.Flag) { opts.set[f.Name] = true })
	setNames(&opts, positional)
	applyCheckDefaults(spec, &opts)
	if err := checkCommandFlags(spec, &opts); err != nil {
		return hostOptions{}, usageFailure(stderr, spec.words, err.Error())
	}
	return opts, exitOK
}

// applyCheckDefaults: repo check with --read-data has no time limit unless
// --timeout is given (Ч27); the data of the storage takes as long as it takes.
func applyCheckDefaults(spec cmdSpec, opts *hostOptions) {
	if spec.offer&offerReadData != 0 && opts.readData > 0 && !opts.set["timeout"] {
		opts.timeout = 0
	}
}

// checkCommandFlags: repo unlock removes nothing without --remove-stale
// (Ч6); then the values of the flags of every command.
func checkCommandFlags(spec cmdSpec, opts *hostOptions) error {
	if spec.offer&offerRemoveStale != 0 && !opts.removeStale {
		return errors.New("--remove-stale is required: the command removes only the stale locks of this host, and only when asked to")
	}
	return checkFlagValues(spec, opts)
}

// checkFlagValues are the checks of the values that follow the parse: the
// source (Р57) and, for repo restore, the target, the paths and the snapshot
// (Р58, Р59, Р61), in the order of Р69. The paths are put in their clean form.
func checkFlagValues(spec cmdSpec, opts *hostOptions) error {
	if spec.offer&offerRestore == 0 {
		return checkSource(*opts)
	}
	if opts.overwrite {
		return errors.New(restore.OverwriteUnavailable)
	}
	paths, err := restore.CheckPaths(opts.paths)
	if err != nil {
		return err
	}
	opts.paths = paths
	for _, err := range []error{restore.CheckTargetFlags(opts.target, opts.allowRoot), checkSource(*opts), restore.CheckRef(opts.address, opts.source)} {
		if err != nil {
			return err
		}
	}
	return nil
}

// checkSource: --source, if given, names a source (Р57).
func checkSource(opts hostOptions) error {
	if !opts.set["source"] {
		return nil
	}
	return restore.CheckSource(opts.source)
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
	case len(positional) > spec.positional+spec.optional:
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
