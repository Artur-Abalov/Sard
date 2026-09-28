// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent enroll (A2b) registers this host as a new agent identity:
// docs/specs/agent/agent-enroll.feature. The mechanism (token parsing, key
// and CSR, trust-on-first-use dial, the Enroll call, atomic file writes) is
// agent/internal/enroll (A2a); this file is the CLI surface — flags,
// messages and the exit-code mapping — layered on top of it.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
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

// clock abstracts time.After so --timeout can be tested without waiting.
type clock interface {
	After(d time.Duration) <-chan time.Time
}

type realEnrollClock struct{}

func (realEnrollClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// runEnroll is "sard-agent enroll ...": args excludes the "enroll" word
// itself. It never reads stdin.
func runEnroll(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc) int {
	return runEnrollWithClock(ctx, args, stdout, stderr, hostname, realEnrollClock{})
}

func runEnrollWithClock(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc, clk clock) int {
	if hasHelpFlag(args) {
		printEnrollHelp(stdout)
		return exitOK
	}
	opts, code := parseEnrollFlags(args, stderr)
	if code != exitOK {
		return code
	}
	return doEnroll(ctx, opts, stdout, stderr, hostname, clk)
}

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
  0  success: identity written, or --help
  1  agent error: an unforeseen server response, or a local problem only an updated agent fixes
  2  usage: bad flags, token source, config, token format, or a --server/config address mismatch
  3  token refused: TOKEN_UNKNOWN, TOKEN_USED, TOKEN_EXPIRED, or TOKEN_REVOKED
  4  identity exists: this host already has a key or certificate; use --force
  5  trust: the server's CA fingerprint or hostname could not be verified
  6  temporary: the server was unreachable, timed out, or another enrollment is already running
  7  write: a target directory or file could not be written
`)
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
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: unexpected argument %q\n", fs.Arg(0))
		return enrollOptions{}, exitUsage
	}
	if *timeout <= 0 {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --timeout must be a positive duration, got %q\n", timeout.String())
		return enrollOptions{}, exitUsage
	}
	if *server != "" {
		if _, _, err := net.SplitHostPort(*server); err != nil {
			_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --server %q is not a valid host:port address\n", *server)
			return enrollOptions{}, exitUsage
		}
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

func resolveEnrollConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return defaultEnrollConfigPath
}

// doEnroll runs the checks in the order В16 fixes: flags (already done by
// the caller) → token source → config → token format → address conflict →
// existing identity → hostname → writable directories → network → CA
// fingerprint → server certificate name → Enroll response → write.
func doEnroll(ctx context.Context, opts enrollOptions, stdout, stderr io.Writer, hostname hostnameFunc, clk clock) int {
	rawToken, cfg, tok, previousAgentID, code := resolveEnrollLocals(opts, stderr)
	if code != exitOK {
		return code
	}
	host, code := resolveEnrollHostname(hostname, stderr)
	if code != exitOK {
		return code
	}
	files := enroll.Files{KeyFile: cfg.TLS.KeyFile, CertFile: cfg.TLS.CertFile, CAFile: cfg.TLS.CAFile}
	unlock, code := lockForWriting(files, stderr)
	if code != exitOK {
		return code
	}
	defer unlock()
	return dialAndEnroll(ctx, cfg, files, tok, rawToken, host, previousAgentID, stdout, stderr, clk, opts.timeout)
}

// resolveEnrollLocals runs every check В16 puts before the hostname check:
// token source → config → token format → address conflict → existing
// identity. None of it touches the network.
func resolveEnrollLocals(opts enrollOptions, stderr io.Writer) (rawToken string, cfg config.Config, tok enroll.Token, previousAgentID string, code int) {
	rawToken, code = resolveTokenSource(opts, stderr)
	if code != exitOK {
		return "", config.Config{}, enroll.Token{}, "", code
	}
	cfg, code = loadEnrollConfig(opts.configPath, stderr)
	if code != exitOK {
		return "", config.Config{}, enroll.Token{}, "", code
	}
	tok, code = parseEnrollToken(rawToken, stderr)
	if code != exitOK {
		return "", config.Config{}, enroll.Token{}, "", code
	}
	if c := checkAddressConflict(opts, cfg, stderr); c != exitOK {
		return "", config.Config{}, enroll.Token{}, "", c
	}
	previousAgentID, code = checkExistingIdentity(cfg, opts.force, stderr)
	if code != exitOK {
		return "", config.Config{}, enroll.Token{}, "", code
	}
	return rawToken, cfg, tok, previousAgentID, exitOK
}

// resolveEnrollHostname reads the OS hostname and checks it locally (В6):
// the length rule, before anything reaches the network.
func resolveEnrollHostname(hostname hostnameFunc, stderr io.Writer) (string, int) {
	host, err := hostname()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: hostname: %v\n", err)
		return "", exitAgentError
	}
	if code := checkHostnameValid(host, stderr); code != exitOK {
		return "", code
	}
	return host, exitOK
}

// lockForWriting is В12 (directories writable) then В15 (single concurrent
// enroll), both local, both before the network.
func lockForWriting(files enroll.Files, stderr io.Writer) (func(), int) {
	if err := enroll.CheckWritable(files); err != nil {
		return nil, reportEnrollError(stderr, err)
	}
	unlock, err := enroll.Lock(files.CertFile)
	if err != nil {
		return nil, reportEnrollError(stderr, err)
	}
	return unlock, exitOK
}

func dialAndEnroll(ctx context.Context, cfg config.Config, files enroll.Files, tok enroll.Token, rawToken, host, previousAgentID string, stdout, stderr io.Writer, clk clock, timeout time.Duration) int {
	ctxTimeout, cancel := deriveTimeoutCtx(ctx, clk, timeout)
	defer cancel()

	conn, err := enroll.DialTOFU(ctxTimeout, cfg.Server.Address, tok.Fingerprint)
	if err != nil {
		return reportEnrollError(stderr, err)
	}
	defer func() { _ = conn.Close() }()

	key, csrDER, code := buildIdentityRequest(host, stderr)
	if code != exitOK {
		return code
	}
	req := &agentv1.EnrollRequest{EnrollmentToken: rawToken, CsrDer: csrDER, Hostname: host}
	result, err := enroll.CallEnroll(ctxTimeout, conn, req)
	if err != nil {
		return reportEnrollError(stderr, err)
	}
	return writeIdentityAndReport(stdout, stderr, cfg, files, key, result, previousAgentID)
}

// buildIdentityRequest generates the fresh key and CSR every enrollment
// sends; the private key never leaves this process as anything but PEM
// written to disk (marshalKeyPEM, in writeIdentityAndReport).
func buildIdentityRequest(host string, stderr io.Writer) (*ecdsa.PrivateKey, []byte, int) {
	key, err := enroll.NewIdentityKey()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
		return nil, nil, exitAgentError
	}
	csrDER, err := enroll.BuildCSR(key, host)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
		return nil, nil, exitAgentError
	}
	return key, csrDER, exitOK
}

func writeIdentityAndReport(stdout, stderr io.Writer, cfg config.Config, files enroll.Files, key *ecdsa.PrivateKey, result *enroll.EnrollResult, previousAgentID string) int {
	keyPEM, err := marshalKeyPEM(key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
		return exitAgentError
	}
	if err := enroll.WriteIdentity(files, keyPEM, []byte(result.CertificateChainPEM), []byte(result.CABundlePEM)); err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: the server enrolled agent %s but writing the identity to disk failed: %v; the enrollment token has been spent, a new one is required\n", result.AgentID, err)
		var eerr *enroll.Error
		errors.As(err, &eerr)
		return enrollExitCode(eerr.Class)
	}
	printEnrollSuccess(stdout, cfg, files, result.AgentID, previousAgentID)
	return exitOK
}

func marshalKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal identity key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func printEnrollSuccess(stdout io.Writer, cfg config.Config, files enroll.Files, agentID, previousAgentID string) {
	_, _ = fmt.Fprintf(stdout, "Enrolled as agent %s\n", agentID)
	_, _ = fmt.Fprintf(stdout, "server: %s\n", cfg.Server.Address)
	_, _ = fmt.Fprintf(stdout, "key:  %s\n", files.KeyFile)
	_, _ = fmt.Fprintf(stdout, "cert: %s\n", files.CertFile)
	_, _ = fmt.Fprintf(stdout, "ca:   %s\n", files.CAFile)
	_, _ = fmt.Fprintln(stdout, "Start or restart the sard-agent service to use this identity.")
	if previousAgentID != "" {
		_, _ = fmt.Fprintf(stdout, "Previous identity %s is now abandoned; revoke it in the console if you no longer need it.\n", previousAgentID)
	}
}

// deriveTimeoutCtx bounds ctx by timeout, measured on clk (real time in
// production, driven by hand in tests — В14's flag changes this wait).
func deriveTimeoutCtx(ctx context.Context, clk clock, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx2, cancel := context.WithCancel(ctx)
	timer := clk.After(timeout)
	go func() {
		select {
		case <-timer:
			cancel()
		case <-ctx2.Done():
		}
	}()
	return ctx2, cancel
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
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: reading --token-file %s: %v\n", path, err)
		return "", exitUsage
	}
	if len(data) == 0 {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: the token file %s is empty\n", path)
		return "", exitUsage
	}
	return enroll.NormalizeTokenFile(data), exitOK
}

func loadEnrollConfig(path string, stderr io.Writer) (config.Config, int) {
	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, config.ErrNoServerAddress) {
			_, _ = fmt.Fprintf(stderr, "sard-agent enroll: server.address is not set; set it in the agent config %s\n", path)
			return config.Config{}, exitUsage
		}
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: reading config %s: %v\n", path, err)
		return config.Config{}, exitUsage
	}
	if key := missingTLSKey(cfg.TLS); key != "" {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: config %s: %s is not set\n", path, key)
		return config.Config{}, exitUsage
	}
	return cfg, exitOK
}

func missingTLSKey(t config.TLS) string {
	for _, f := range []struct{ key, val string }{
		{"tls.ca_file", t.CAFile},
		{"tls.cert_file", t.CertFile},
		{"tls.key_file", t.KeyFile},
	} {
		if f.val == "" {
			return f.key
		}
	}
	return ""
}

func parseEnrollToken(raw string, stderr io.Writer) (enroll.Token, int) {
	tok, err := enroll.ParseToken(raw)
	if err != nil {
		var terr *enroll.TokenError
		if errors.As(err, &terr) {
			_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %s: %s\n", terr.Reason, reasonMeaning(terr.Reason))
		} else {
			_, _ = fmt.Fprintf(stderr, "sard-agent enroll: invalid token: %v\n", err)
		}
		return enroll.Token{}, exitUsage
	}
	return tok, exitOK
}

func checkAddressConflict(opts enrollOptions, cfg config.Config, stderr io.Writer) int {
	if opts.serverSet && !config.AddressEqual(opts.server, cfg.Server.Address) {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --server %s does not match server.address %s in the config; enroll never writes to the config — fix one of the two\n", opts.server, cfg.Server.Address)
		return exitUsage
	}
	return exitOK
}

// checkExistingIdentity is В19/rule "Существующая идентичность без --force
// не трогается": returns the agent_id to name as abandoned in the success
// message when --force replaces an existing identity.
func checkExistingIdentity(cfg config.Config, force bool, stderr io.Writer) (previousAgentID string, code int) {
	status, err := enroll.InspectIdentity(cfg.TLS)
	if err != nil {
		return "", reportIdentityInspectionError(stderr, err)
	}
	if !status.Exists {
		return "", exitOK
	}
	if force {
		return status.AgentID, exitOK
	}
	printExistingIdentityRefusal(stderr, cfg, status)
	return "", exitIdentityExists
}

func reportIdentityInspectionError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent enroll: checking the existing identity: %v\n", err)
	return exitAgentError
}

func printExistingIdentityRefusal(stderr io.Writer, cfg config.Config, status enroll.IdentityStatus) {
	if status.Unreadable {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: this host already has an identity but its agent_id could not be read from %s; use --force to enroll a new identity, or re-enroll keeping the identity (not available yet)\n", cfg.TLS.CertFile)
		return
	}
	id := status.AgentID
	if id == "" {
		id = "(unknown)"
	}
	_, _ = fmt.Fprintf(stderr, "sard-agent enroll: this host is already enrolled as agent %s at %s; use --force to enroll a new identity, or re-enroll keeping the identity (not available yet)\n", id, cfg.Server.Address)
}

const (
	minHostnameLen = 1
	maxHostnameLen = 253
)

func checkHostnameValid(host string, stderr io.Writer) int {
	if len(host) >= minHostnameLen && len(host) <= maxHostnameLen {
		return exitOK
	}
	_, _ = fmt.Fprintln(stderr, "sard-agent enroll: HOSTNAME_INVALID: the host name reported by the operating system is empty or longer than 253 characters")
	return exitAgentError
}

// enrollExitCode is the single place A2b maps an enroll.Class to an exit
// code (В3, В12 of the task).
// enrollExitCodes is the one table A2b maps enroll.Class to an exit code
// from (В3, В12 of the task).
var enrollExitCodes = map[enroll.Class]int{
	enroll.ClassAgentError:   exitAgentError,
	enroll.ClassUsage:        exitUsage,
	enroll.ClassTokenRefused: exitTokenRefused,
	enroll.ClassTrust:        exitTrust,
	enroll.ClassTemporary:    exitTemporary,
	enroll.ClassWrite:        exitWrite,
}

func enrollExitCode(class enroll.Class) int {
	if code, ok := enrollExitCodes[class]; ok {
		return code
	}
	return exitAgentError
}

func reportEnrollError(stderr io.Writer, err error) int {
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
		return exitAgentError
	}
	_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %s\n", enrollMessage(eerr))
	return enrollExitCode(eerr.Class)
}

// enrollMessage adds what *enroll.Error's own Error() text leaves out — the
// address, server certificate names, gRPC code and "token maybe spent"
// warning are structured fields, not part of the string A2a builds — plus,
// for reasons A2a does not itself explain, what each one means to the
// operator and whether retrying with the same token can help.
func enrollMessage(e *enroll.Error) string {
	return e.Error() + reasonMeaningSuffix(e) + addressSuffix(e) + namesSuffix(e) + codeSuffix(e) + spentSuffix(e)
}

// reasonMeaningSuffix skips ClassAgentError: its own msg (В4) already says
// what reasonMeaning would, in the agent-fault wording, not the "get a new
// token" wording reasonMeaning uses for the rest.
func reasonMeaningSuffix(e *enroll.Error) string {
	if e.Class == enroll.ClassAgentError {
		return ""
	}
	if meaning := reasonMeaning(e.Reason); meaning != "" {
		return ": " + meaning
	}
	return ""
}

func addressSuffix(e *enroll.Error) string {
	if e.Address == "" {
		return ""
	}
	return fmt.Sprintf(" (server address %s)", e.Address)
}

func namesSuffix(e *enroll.Error) string {
	if len(e.Names) == 0 {
		return ""
	}
	return "; server certificate names: " + strings.Join(e.Names, ", ")
}

func codeSuffix(e *enroll.Error) string {
	if e.Code == "" {
		return ""
	}
	return fmt.Sprintf("; gRPC code %s", e.Code)
}

func spentSuffix(e *enroll.Error) string {
	if !e.TokenMaybeSpent {
		return ""
	}
	return "; the token may have been spent by this attempt — if a retry is refused with TOKEN_USED, get a new token"
}

// reasonMeaning explains a server refusal reason and whether retrying with
// the same token can help (rule "Отказы сервера объясняются по причине").
func reasonMeaning(reason string) string {
	switch reason {
	case "TOKEN_UNKNOWN":
		return "this token was not issued by this server; retrying with it will not help"
	case "TOKEN_USED":
		return "this token has already been used; retrying with it will not help, get a new token"
	case "TOKEN_EXPIRED":
		return "this token has expired; retrying with it will not help, get a new token"
	case "TOKEN_REVOKED":
		return "this token was revoked in the console; retrying with it will not help"
	case "TOKEN_MALFORMED":
		return "the token string looks corrupted from copying; retrying with it will not help"
	default:
		return ""
	}
}
