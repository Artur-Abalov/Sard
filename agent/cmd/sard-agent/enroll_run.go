// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent enroll (A2b) registers this host as a new agent identity:
// docs/specs/agent/agent-enroll.feature. The mechanism (token parsing, key
// and CSR, trust-on-first-use dial, the Enroll call, atomic file writes) is
// agent/internal/enroll (A2a); these files are the CLI surface layered on
// top of it, split by concern (F11): enroll_flags.go (flags, --help, token
// source), enroll_run.go (this file: the В16 pipeline), enroll_report.go
// (messages, the exit-code table).
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// clock abstracts time.After so --timeout can be tested without waiting.
type clock interface {
	After(d time.Duration) <-chan time.Time
	// Now is the time on this clock; repo add uses it to leave the time
	// the operator spends at the terminal out of --timeout.
	Now() time.Time
}

type realEnrollClock struct{}

func (realEnrollClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realEnrollClock) Now() time.Time { return time.Now() }

// enrollDeps is every dependency doEnroll's pipeline reaches outside its
// own arguments: production always builds the real ones (runEnroll);
// tests substitute a fake hostname, a hand-driven clock (--timeout,
// enroll_timing_test.go), or a dial function that avoids the real network
// (F6, enroll_fake_test.go) — without which "the server's name does not
// resolve" and the two IPv6 scenarios could not be tested honestly.
type enrollDeps struct {
	hostname hostnameFunc
	clock    clock
	dial     enroll.DialFunc
	// euid, lookupUser and fs decide who runs the command and hand what it
	// writes to the service user (A8a, Р25).
	euid       uint32
	lookupUser hostsetup.LookupFunc
	fs         hostsetup.FS
	// chownFile gives an open file to the service user (fchown).
	chownFile func(f *os.File, uid, gid int) error
}

func productionEnrollDeps(hostname hostnameFunc) enrollDeps {
	return enrollDeps{
		hostname: hostname, clock: realEnrollClock{}, dial: enroll.RealDial,
		euid: uint32(os.Geteuid()), lookupUser: hostsetup.LookupOS, fs: hostsetup.OS{}, chownFile: chownOpenFile,
	}
}

// runEnroll is "sard-agent enroll ...": args excludes the "enroll" word
// itself. It never reads stdin.
func runEnroll(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc) int {
	return runEnrollWithDeps(ctx, args, stdout, stderr, productionEnrollDeps(hostname))
}

func runEnrollWithDeps(ctx context.Context, args []string, stdout, stderr io.Writer, deps enrollDeps) int {
	if hasHelpFlag(args) {
		printEnrollHelp(stdout)
		return exitOK
	}
	opts, code := parseEnrollFlags(args, stderr)
	if code != exitOK {
		return code
	}
	who, f := hostsetup.Authorize(privilegeRequest(deps.euid, deps.lookupUser, opts.configPath, "enroll --server <address> --token <token>", false, true))
	if f != nil {
		return report(stderr, "enroll", f)
	}
	return doEnroll(ctx, opts, who, stdout, stderr, deps)
}

// enrollPipelineState is what one pipeline call accumulates as it passes
// the checks in resolveEnrollLocals, then the hostname check, before it
// ever reaches the network — bundled together so dialAndEnroll and
// resolveEnrollLocals take one value each instead of a long parameter
// list (F11).
type enrollPipelineState struct {
	rawToken        string
	cfg             config.Config
	tok             enroll.Token
	previousAgentID string
	host            string
	files           enroll.Files
	// owner is who the files belong to: the service user under root, nil
	// when the command runs as the service user.
	owner *enroll.Owner
}

// doEnroll runs the checks in the order В16 fixes: flags (already done by
// the caller) → token source → config → token format → address conflict →
// existing identity → hostname → writable directories → existing identity
// again (F2, now that the lock rules out a race) → network → CA
// fingerprint → server certificate name → Enroll response → write.
func doEnroll(ctx context.Context, opts enrollOptions, who hostsetup.Principal, stdout, stderr io.Writer, deps enrollDeps) (code int) {
	st, code := resolveEnrollLocals(opts, stderr)
	if code != exitOK {
		return code
	}
	host, code := resolveEnrollHostname(deps.hostname, stderr)
	if code != exitOK {
		return code
	}
	st.host = host
	st.files = enroll.Files{KeyFile: st.cfg.TLS.KeyFile, CertFile: st.cfg.TLS.CertFile, CAFile: st.cfg.TLS.CAFile}
	st.owner = enrollOwner(who, deps)
	// Р25а: under root the missing last directory of a tls.* path is made;
	// a command that does not succeed takes it away again (it runs after
	// the lock is released, the lock file lives in one of these directories).
	created, code := ensureTLSDirs(st.files, who, deps, stderr)
	defer func() {
		if code != exitOK {
			removeTLSDirs(deps.fs, created)
		}
	}()
	if code != exitOK {
		return code
	}
	return enrollWritable(ctx, st, opts, stdout, stderr, deps)
}

// enrollWritable is the rest of doEnroll, once the directories of tls.*
// are there: their writability, the lock, the identity check again and
// the network.
func enrollWritable(ctx context.Context, st enrollPipelineState, opts enrollOptions, stdout, stderr io.Writer, deps enrollDeps) int {
	unlock, code := lockForWriting(st.files, stderr)
	if code != exitOK {
		return code
	}
	defer unlock()
	// F2: the identity check above ran before the lock was acquired, so a
	// second enroll racing this one could have passed it too and be about
	// to overwrite the identity this one is about to write. Re-checking
	// now that the lock is held closes that window; the message and exit
	// code are the same "already enrolled" refusal as the first check.
	if _, code := checkExistingIdentity(st.cfg, opts.force, stderr); code != exitOK {
		return code
	}
	return dialAndEnroll(ctx, st, stdout, stderr, deps, opts.timeout)
}

// resolveEnrollLocals runs every check В16 puts before the hostname check:
// token source → config → token format → address conflict → existing
// identity. None of it touches the network.
func resolveEnrollLocals(opts enrollOptions, stderr io.Writer) (enrollPipelineState, int) {
	rawToken, code := resolveTokenSource(opts, stderr)
	if code != exitOK {
		return enrollPipelineState{}, code
	}
	cfg, code := loadEnrollConfig(opts.configPath, stderr)
	if code != exitOK {
		return enrollPipelineState{}, code
	}
	tok, code := parseAndCheckToken(rawToken, opts, cfg, stderr)
	if code != exitOK {
		return enrollPipelineState{}, code
	}
	previousAgentID, code := checkExistingIdentity(cfg, opts.force, stderr)
	if code != exitOK {
		return enrollPipelineState{}, code
	}
	return enrollPipelineState{rawToken: rawToken, cfg: cfg, tok: tok, previousAgentID: previousAgentID}, exitOK
}

// parseAndCheckToken parses the token, then checks the address against the
// config.
func parseAndCheckToken(rawToken string, opts enrollOptions, cfg config.Config, stderr io.Writer) (enroll.Token, int) {
	tok, code := parseEnrollToken(rawToken, stderr)
	if code != exitOK {
		return tok, code
	}
	return tok, checkAddressConflict(opts, cfg, stderr)
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

func dialAndEnroll(ctx context.Context, st enrollPipelineState, stdout, stderr io.Writer, deps enrollDeps, timeout time.Duration) int {
	ctxTimeout, cancel := deriveTimeoutCtx(ctx, deps.clock, timeout)
	defer cancel()

	conn, err := enroll.DialTOFU(ctxTimeout, deps.dial, st.cfg.Server.Address, st.tok.Fingerprint)
	if err != nil {
		return reportEnrollError(stderr, err)
	}
	defer func() { _ = conn.Close() }()

	key, csrDER, code := buildIdentityRequest(st.host, stderr)
	if code != exitOK {
		return code
	}
	req := &agentv1.EnrollRequest{EnrollmentToken: st.rawToken, CsrDer: csrDER, Hostname: st.host}
	result, err := enroll.CallEnroll(ctxTimeout, conn, req)
	if err != nil {
		return reportEnrollError(stderr, err)
	}
	return writeIdentityAndReport(stdout, stderr, st, key, result)
}

// buildIdentityRequest generates the fresh key and CSR every enrollment
// sends; the private key never leaves this process as anything but PEM
// written to disk (marshalKeyPEM, in writeIdentityAndReport).
func buildIdentityRequest(host string, stderr io.Writer) (*ecdsa.PrivateKey, []byte, int) {
	key, err := enroll.NewIdentityKey()
	if err != nil {
		return nil, nil, reportAgentError(stderr, err)
	}
	csrDER, err := enroll.BuildCSR(key, host)
	if err != nil {
		return nil, nil, reportAgentError(stderr, err)
	}
	return key, csrDER, exitOK
}

// reportAgentError prints a plain local error (key generation, CSR, PEM
// marshalling — never a server response, which reportEnrollError handles)
// and returns the exit code every one of them shares (В3): the agent's
// own fault, not the operator's.
func reportAgentError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
	return exitAgentError
}

func writeIdentityAndReport(stdout, stderr io.Writer, st enrollPipelineState, key *ecdsa.PrivateKey, result *enroll.EnrollResult) int {
	cfg, files, previousAgentID := st.cfg, st.files, st.previousAgentID
	keyPEM, err := marshalKeyPEM(key)
	if err != nil {
		return reportAgentError(stderr, err)
	}
	if err := enroll.WriteIdentityAs(files, keyPEM, []byte(result.CertificateChainPEM), []byte(result.CABundlePEM), st.owner); err != nil {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: the server enrolled agent %s but writing the identity to disk failed: %v; the enrollment token has been spent, a new one is required\n", result.AgentID, err)
		// F11: errors.As's result was ignored here, so a WriteIdentity
		// failure that was not a *enroll.Error (none is today, but nothing
		// enforces that) would dereference a nil eerr. Same safe pattern as
		// reportEnrollError (enroll_report.go).
		var eerr *enroll.Error
		if errors.As(err, &eerr) {
			return enrollExitCode(eerr.Class)
		}
		return exitWrite
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

func loadEnrollConfig(path string, stderr io.Writer) (config.Config, int) {
	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, config.ErrNoServerAddress) {
			_, _ = fmt.Fprintf(stderr, "sard-agent enroll: server.address is not set; set it in the agent config %s\n", redactIfToken(path))
			return config.Config{}, exitUsage
		}
		// A *PathError's own text repeats path — redactIfToken handles
		// both independently, wherever in the string a token lands (В2).
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: reading config %s: %s\n", redactIfToken(path), redactIfToken(err.Error()))
		return config.Config{}, exitUsage
	}
	if key := missingTLSKey(cfg.TLS); key != "" {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: config %s: %s is not set\n", redactIfToken(path), key)
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
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: --server %s does not match server.address %s in the config; enroll never writes to the config — fix one of the two\n", redactIfToken(opts.server), cfg.Server.Address)
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
