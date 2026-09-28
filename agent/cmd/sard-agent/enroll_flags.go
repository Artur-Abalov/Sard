// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// Exit codes past exitOK/exitError/exitUsage (В3, docs/specs/agent/agent-enroll.feature).
const (
	exitAgentError     = exitError // 1: CSR_INVALID, HOSTNAME_INVALID, an unforeseen response.
	exitTokenRefused   = 3         // TOKEN_UNKNOWN, TOKEN_USED, TOKEN_EXPIRED, TOKEN_REVOKED.
	exitIdentityExists = 4         // a key or certificate already exists, --force not given.
	exitTrust          = 5         // CA fingerprint, hostname, other TLS failures, TOKEN_FOREIGN_CA.
	exitTemporary      = 6         // server unreachable, timeout, INTERNAL_RETRYABLE, interrupted, locked.
	exitWrite          = 7         // a target directory is not writable, or writing the files failed.
)

// defaultEnrollConfigPath is the path the sard-agent service unit passes
// (В7); enroll uses the same default when --config is not given.
const defaultEnrollConfigPath = "/etc/sard/agent.yaml"

// defaultEnrollTimeout bounds the whole operation unless --timeout says
// otherwise (В14).
const defaultEnrollTimeout = 30 * time.Second

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}

func printEnrollHelp(stdout io.Writer) {
	_, _ = fmt.Fprint(stdout, `Usage: sard-agent enroll --server <address> --token <token> [flags]

Registers this host as a new agent identity with sard-server and writes the
key, certificate and CA bundle named by tls.* in the agent config. Never
reads stdin and never changes the config file.

Flags:
  --server string      server address; if given, must match server.address in --config
  --token string        enrollment token
  --token-file string   file holding the enrollment token
  --force                enroll a new identity even if one already exists
  --config string        path to the agent config (default /etc/sard/agent.yaml)
  --timeout duration      how long to wait for the server (default 30s)

The enrollment token is given exactly one way: --token, --token-file, or the
SARD_ENROLL_TOKEN environment variable.

Exit codes:
`)
	for _, c := range enrollHelpCodes() {
		_, _ = fmt.Fprintf(stdout, "  %d  %s: %s\n", c.code, c.word, c.help)
	}
}

// enrollOptions is what parseEnrollFlags resolves before any local file or
// network check runs.
type enrollOptions struct {
	server       string
	serverSet    bool
	token        string
	tokenSet     bool
	tokenFile    string
	tokenFileSet bool
	force        bool
	timeout      time.Duration
	configPath   string
}

func parseEnrollFlags(args []string, stderr io.Writer) (enrollOptions, int) {
	fs := flag.NewFlagSet("sard-agent enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	server := fs.String("server", "", "")
	token := fs.String("token", "", "")
	tokenFile := fs.String("token-file", "", "")
	force := fs.Bool("force", false, "")
	timeout := fs.Duration("timeout", defaultEnrollTimeout, "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		return enrollOptions{}, exitUsage
	}
	if fs.NArg() > 0 {
		// F3: never echo a positional argument — a token pasted without
		// --token would land here, and the token must never appear in
		// output (В2), not even partially.
		_, _ = fmt.Fprintln(stderr, "sard-agent enroll: unexpected extra argument")
		return enrollOptions{}, exitUsage
	}
	if code := validateEnrollFlagValues(*timeout, *server, stderr); code != exitOK {
		return enrollOptions{}, code
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return enrollOptions{
		server: *server, serverSet: set["server"],
		token: *token, tokenSet: set["token"],
		tokenFile: *tokenFile, tokenFileSet: set["token-file"],
		force:      *force,
		timeout:    *timeout,
		configPath: resolveEnrollConfigPath(*configPath),
	}, exitOK
}

// validateEnrollFlagValues checks the parsed flag values that flag.Parse
// itself cannot reject: a --timeout that isn't positive, and a --server
// that isn't a valid host:port address.
func validateEnrollFlagValues(timeout time.Duration, server string, stderr io.Writer) int {
	if timeout <= 0 {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --timeout must be a positive duration, got %q\n", timeout.String())
		return exitUsage
	}
	if server == "" {
		return exitOK
	}
	if _, _, err := net.SplitHostPort(server); err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --server %q is not a valid host:port address\n", server)
		return exitUsage
	}
	return exitOK
}

func resolveEnrollConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return defaultEnrollConfigPath
}

// resolveTokenSource picks exactly one of --token, --token-file and
// SARD_ENROLL_TOKEN (rule 2, В11): an empty environment variable counts as
// unset, an empty --token counts as set (and malformed).
func resolveTokenSource(opts enrollOptions, stderr io.Writer) (string, int) {
	envVal, envSet := lookupEnrollTokenEnv()
	sources := tokenSourceNames(opts, envSet)
	switch len(sources) {
	case 0:
		_, _ = fmt.Fprintln(stderr, "sard-agent enroll: no enrollment token given; use exactly one of --token, --token-file, or the SARD_ENROLL_TOKEN environment variable")
		return "", exitUsage
	case 1:
		return readFromTokenSource(sources[0], opts, envVal, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: conflicting token sources: %s; use exactly one\n", strings.Join(sources, " and "))
		return "", exitUsage
	}
}

// lookupEnrollTokenEnv treats an empty SARD_ENROLL_TOKEN as unset (В11).
func lookupEnrollTokenEnv() (string, bool) {
	val, present := os.LookupEnv("SARD_ENROLL_TOKEN")
	return val, present && val != ""
}

func tokenSourceNames(opts enrollOptions, envSet bool) []string {
	var sources []string
	if opts.tokenSet {
		sources = append(sources, "--token")
	}
	if opts.tokenFileSet {
		sources = append(sources, "--token-file")
	}
	if envSet {
		sources = append(sources, "SARD_ENROLL_TOKEN")
	}
	return sources
}

func readFromTokenSource(source string, opts enrollOptions, envVal string, stderr io.Writer) (string, int) {
	switch source {
	case "--token":
		return opts.token, exitOK
	case "--token-file":
		return readTokenFile(opts.tokenFile, stderr)
	default:
		return envVal, exitOK
	}
}

func readTokenFile(path string, stderr io.Writer) (string, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		// A *PathError's own text repeats path — printed unredacted, it
		// would undo redactIfToken(path) right next to it (F3).
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: reading --token-file %s: %s\n", redactIfToken(path), redactedFileError(path, err))
		return "", exitUsage
	}
	if len(data) == 0 {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: the token file %s is empty\n", redactIfToken(path))
		return "", exitUsage
	}
	return enroll.NormalizeTokenFile(data), exitOK
}

// redactIfToken hides operator input that looks like a pasted enrollment
// token string (starts with "sard_") before it is echoed back in a
// message (F3): the token must never appear in output (В2), even when the
// operator passed it in the wrong place — e.g. as a --token-file path.
func redactIfToken(s string) string {
	if strings.HasPrefix(s, "sard_") {
		return "<redacted: looks like a token>"
	}
	return s
}

// redactedFileError is err's own text, unless path looks like a token: a
// *PathError's text always repeats the path verbatim, which would leak it
// right next to redactIfToken(path) in the same message.
func redactedFileError(path string, err error) string {
	if strings.HasPrefix(path, "sard_") {
		return "could not be read"
	}
	return err.Error()
}
