// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
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

Run it with sudo: sudo sard-agent enroll --server <address> --token <token>.
The files tls.* then belong to the service user (sard-agent, or service.user
of the config), the key readable by it alone (0600); a missing last
directory of a tls.* path is created for it (0700), a missing parent is not.
Run as the service user itself it works as it always did. Anyone else is
refused with PRIVILEGES_REQUIRED, exit code 2, before the token is read.

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
	// В2/F3: flag's own error output would echo a bad flag value verbatim
	// (e.g. "invalid value \"sard_...\" for flag -timeout") if a token
	// lands in the wrong flag by mistake — discarded here, and replaced
	// below with the same, redacted, text through our own writer.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	server := fs.String("server", "", "")
	token := fs.String("token", "", "")
	tokenFile := fs.String("token-file", "", "")
	force := fs.Bool("force", false, "")
	timeout := fs.Duration("timeout", defaultEnrollTimeout, "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %s\n", redactIfToken(err.Error()))
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
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --server %q is not a valid host:port address\n", redactIfToken(server))
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
		// A *PathError's own text repeats path — redactIfToken handles
		// both, so the path and the underlying OS error text are each
		// redacted independently, wherever in the string a token lands.
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: reading --token-file %s: %s\n", redactIfToken(path), redactIfToken(err.Error()))
		return "", exitUsage
	}
	if len(data) == 0 {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: the token file %s is empty\n", redactIfToken(path))
		return "", exitUsage
	}
	return enroll.NormalizeTokenFile(data), exitOK
}

// tokenPattern matches an enrollment token string (docs/specs/enrollment-token.md)
// anywhere it appears in a larger string — not just when the whole string
// is one — so it also catches a token embedded in flag.Parse's or
// *PathError's own error text ("invalid value \"sard_...\" for flag
// -timeout: ...", "open sard_...: no such file or directory"). It follows
// the token's alphabet — the secret in base64url, then "." and the hex
// fingerprint — so the characters around a token stay. A path such as
// /etc/sard_agent/x is redacted as well; the owner accepted that (OQ-026).
var tokenPattern = regexp.MustCompile(`sard_[A-Za-z0-9_-]+(?:\.[0-9a-fA-F]*)?`)

// redactIfToken hides every enrollment-token-looking substring (starting
// "sard_") in s before it is echoed back in a message (В2, F3): the token
// must never appear in output, even when the operator passed it in the
// wrong place — a --token-file path, a --server or --config value, a
// --timeout that fails to parse as a duration, a stray positional
// argument.
func redactIfToken(s string) string {
	return tokenPattern.ReplaceAllString(s, "<redacted: looks like a token>")
}
