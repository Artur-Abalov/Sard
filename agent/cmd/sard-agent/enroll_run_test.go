// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

// Direct, white-box tests of enroll_run.go's pipeline functions and their
// early returns — some order/short-circuit guarantees are only observable
// as a side effect (a function that must never run), not as a difference
// in the final exit code, because a later, legitimately redundant check
// (F2's second existing-identity check) would otherwise produce the same
// final result regardless.

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// runEnrollWithDeps must stop at a flag-parsing failure and never reach
// doEnroll: a nil deps.hostname (production always sets it) would panic
// the moment resolveEnrollHostname called it, so a panic here proves the
// pipeline was entered when it must not have been.
func TestRunEnrollWithDepsStopsAtAFlagParsingFailure(t *testing.T) {
	var out, errOut strings.Builder
	code := runEnrollWithDeps(context.Background(), []string{"--timeout", "not-a-duration"}, &out, &errOut, enrollDeps{})
	if code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
	// doEnroll (which runEnrollWithDeps must never reach here) would fail
	// on a zero-value enrollOptions too, coincidentally with the same
	// exitUsage code but a completely different message ("no enrollment
	// token given") — the flag-parsing error text is what proves the
	// early return actually fired, not a downstream check happening to
	// agree on the exit code.
	if !strings.Contains(errOut.String(), "invalid value") || !strings.Contains(errOut.String(), "-timeout") {
		t.Fatalf("stderr = %q, want the flag package's own parse-error text", errOut.String())
	}
	if strings.Contains(errOut.String(), "no enrollment token given") {
		t.Fatalf("stderr = %q, want the flag error, not doEnroll's downstream token-source message", errOut.String())
	}
}

// resolveEnrollLocals must check the token source before ever calling
// loadEnrollConfig — proven by a config path that would be read (and fail
// differently, "reading config", not "no enrollment token given") if the
// token-source check's early return were skipped.
func TestResolveEnrollLocalsChecksTheTokenSourceBeforeTheConfig(t *testing.T) {
	missing := t.TempDir() + "/does-not-exist.yaml"
	opts := enrollOptions{configPath: missing, timeout: defaultEnrollTimeout}
	// No token source set at all.
	var stderr strings.Builder
	_, code := resolveEnrollLocals(opts, &stderr)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
	if !strings.Contains(stderr.String(), "no enrollment token given") {
		t.Fatalf("stderr = %q, want the token-source message (checked before the config)", stderr.String())
	}
	// resolveTokenSource's own message alone can appear even if
	// resolveEnrollLocals kept going afterward and loadEnrollConfig also
	// failed and printed its own message too — the absence of that
	// second message is what proves the early return actually stopped
	// the pipeline right after the token-source check.
	if strings.Contains(stderr.String(), "reading config") {
		t.Fatalf("stderr = %q, want only the token-source message: loadEnrollConfig must never have run", stderr.String())
	}
}

// The existing-identity check must run before the hostname lookup — an
// existing identity must refuse without ever calling hostname(), which the
// second, redundant existing-identity check inside doEnroll (F2) would
// otherwise mask from an exit-code-only assertion.
func TestExistingIdentityIsCheckedBeforeTheHostnameLookup(t *testing.T) {
	f := newLocalFixture(t)
	writeAgentCertForTest(t, f.h.certFile, "existing-agent")
	var calls atomic.Int32
	countingHostname := func() (string, error) {
		calls.Add(1)
		return "db1", nil
	}
	code, _, errOut := runEnrollCmdOn(countingHostname, "--config", f.h.configPath, "--token", f.token)
	if code != exitIdentityExists {
		t.Fatalf("code = %d, want %d; stderr = %q", code, exitIdentityExists, errOut)
	}
	if calls.Load() != 0 {
		t.Fatalf("hostname() was called %d times, want 0: the existing-identity check must run first", calls.Load())
	}
}

// resolveEnrollHostname must report the underlying hostname() failure by
// name, and stop there (host must not fall through to checkHostnameValid
// as if it were empty and unrelated).
func TestResolveEnrollHostnameReportsTheUnderlyingFailure(t *testing.T) {
	broken := func() (string, error) { return "", errors.New("uname failed") }
	var stderr strings.Builder
	host, code := resolveEnrollHostname(broken, &stderr)
	if code != exitAgentError {
		t.Fatalf("code = %d, want exitAgentError", code)
	}
	if host != "" {
		t.Fatalf("host = %q, want empty on failure", host)
	}
	if !strings.Contains(stderr.String(), "uname failed") {
		t.Fatalf("stderr = %q, want it to contain the underlying hostname error", stderr.String())
	}
	if strings.Contains(stderr.String(), "HOSTNAME_INVALID") {
		t.Fatalf("stderr = %q, want the hostname()-failure message, not HOSTNAME_INVALID", stderr.String())
	}
}

func TestCheckHostnameValidBoundaries(t *testing.T) {
	cases := []struct {
		name string
		host string
		ok   bool
	}{
		{"empty", "", false},
		{"one char", "a", true},
		{"253 chars", strings.Repeat("a", 253), true},
		{"254 chars", strings.Repeat("a", 254), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code := checkHostnameValid(c.host, io.Discard)
			if c.ok && code != exitOK {
				t.Fatalf("code = %d, want exitOK for a %d-char host", code, len(c.host))
			}
			if !c.ok && code == exitOK {
				t.Fatalf("code = exitOK, want a rejection for a %d-char host", len(c.host))
			}
		})
	}
}

// checkExistingIdentity must surface an InspectIdentity error (a broken
// tls.cert_file parent path here) rather than silently treating it as "no
// identity".
func TestCheckExistingIdentityReportsAnInspectionError(t *testing.T) {
	dir := t.TempDir()
	notADir := dir + "/not-a-dir"
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{TLS: config.TLS{CertFile: notADir + "/cert.pem", KeyFile: dir + "/key.pem", CAFile: dir + "/ca.pem"}}
	var stderr strings.Builder
	_, code := checkExistingIdentity(cfg, false, &stderr)
	if code != exitAgentError {
		t.Fatalf("code = %d, want exitAgentError", code)
	}
	if !strings.Contains(stderr.String(), "checking the existing identity") {
		t.Fatalf("stderr = %q, want it to say the identity check itself failed", stderr.String())
	}
}

// printExistingIdentityRefusal must say "(unknown)" when AgentID could not
// be read (a key-only identity, say), not an empty agent id in the middle
// of the sentence.
func TestPrintExistingIdentityRefusalUsesUnknownForAnEmptyAgentID(t *testing.T) {
	cfg := config.Config{Server: config.Server{Address: "sard.example.com:9090"}}
	var stderr strings.Builder
	printExistingIdentityRefusal(&stderr, cfg, enroll.IdentityStatus{Exists: true, AgentID: ""})
	if !strings.Contains(stderr.String(), "(unknown)") {
		t.Fatalf("stderr = %q, want it to say (unknown)", stderr.String())
	}
}

// loadEnrollConfig must report a missing-server-address config distinctly
// from an unrelated read/parse failure.
func TestLoadEnrollConfigReportsAMissingServerAddress(t *testing.T) {
	path := writeConfig(t, "tls:\n  ca_file: /x\n  cert_file: /y\n  key_file: /z\n")
	var stderr strings.Builder
	_, code := loadEnrollConfig(path, &stderr)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
	if !strings.Contains(stderr.String(), "server.address is not set") {
		t.Fatalf("stderr = %q, want the missing-server-address message", stderr.String())
	}
}

func TestLoadEnrollConfigReportsAReadFailure(t *testing.T) {
	missing := t.TempDir() + "/does-not-exist.yaml"
	var stderr strings.Builder
	_, code := loadEnrollConfig(missing, &stderr)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
	if !strings.Contains(stderr.String(), "reading config") {
		t.Fatalf("stderr = %q, want the read-failure message", stderr.String())
	}
}

// reportAgentError and reportIdentityInspectionError: direct message checks.
func TestReportAgentErrorPrintsTheError(t *testing.T) {
	var stderr strings.Builder
	code := reportAgentError(&stderr, errors.New("boom"))
	if code != exitAgentError || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestReportIdentityInspectionErrorPrintsTheError(t *testing.T) {
	var stderr strings.Builder
	code := reportIdentityInspectionError(&stderr, errors.New("boom"))
	if code != exitAgentError || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
}

// buildIdentityRequest must propagate a key-generation failure without
// proceeding, using the same GODEBUG-gated technique
// agent/internal/enroll's own tests use (crypto/rand.Reader is ignored by
// ecdsa.GenerateKey/x509.CreateCertificateRequest since Go 1.26 unless
// GODEBUG=cryptocustomrand=1 is set).
func TestBuildIdentityRequestPropagatesAKeyGenerationFailure(t *testing.T) {
	t.Setenv("GODEBUG", "cryptocustomrand=1")
	old := rand.Reader
	rand.Reader = failingReaderForTest{}
	defer func() { rand.Reader = old }()

	var stderr strings.Builder
	key, der, code := buildIdentityRequest("db1", &stderr)
	if code != exitAgentError || key != nil || der != nil {
		t.Fatalf("code = %d, key = %v, der = %v", code, key, der)
	}
	if !strings.Contains(stderr.String(), "generate key") {
		t.Fatalf("stderr = %q, want it to name the key-generation failure", stderr.String())
	}
}

type failingReaderForTest struct{}

func (failingReaderForTest) Read([]byte) (int, error) {
	return 0, errors.New("simulated entropy failure")
}

// The hostname read at the top of doEnroll must be the one that ends up in
// the CSR's CommonName and the Enroll request's Hostname field — a lost
// assignment (st.host never actually set) would leave both empty
// downstream.
func TestTheHostnameReadEarlyReachesTheCSRAndTheRequest(t *testing.T) {
	ca := newTestCA(t)
	var gotHostname string
	var gotCN string
	answer := func(req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		gotHostname = req.GetHostname()
		csr, err := x509.ParseCertificateRequest(req.GetCsrDer())
		if err != nil {
			t.Fatal(err)
		}
		gotCN = csr.Subject.CommonName
		certPEM, err := ca.issueAgentCert("a1", csr.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		return &agentv1.EnrollResponse{AgentId: "a1", CertificateChainPem: certPEM, CaBundlePem: ca.pem()}, nil
	}
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
	srv := &enrollServer{answer: answer}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	distinctiveHostname := func() (string, error) { return "distinctive-host-name", nil }
	code, _, errOut := runEnrollCmdOn(distinctiveHostname, "--config", h.configPath, "--token", token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	if gotHostname != "distinctive-host-name" {
		t.Fatalf("server saw Hostname = %q, want distinctive-host-name", gotHostname)
	}
	if gotCN != "distinctive-host-name" {
		t.Fatalf("CSR CommonName = %q, want distinctive-host-name", gotCN)
	}
}

// dialAndEnroll must close conn on every path, including success — a
// deferred Close that is never actually called would leak the TLS
// connection to the fake server forever.
func TestASuccessfulEnrollClosesTheConnection(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	before := openFDCountForTest(t)
	for i := 0; i < 20; i++ {
		f2 := newSucceedingFakeFixture(t, "a1")
		code, _, errOut := runEnrollCmdTest("--config", f2.h.configPath, "--token", f2.token)
		if code != exitOK {
			t.Fatalf("code = %d, stderr = %q", code, errOut)
		}
	}
	_ = f
	after := openFDCountForTest(t)
	// Generous margin: each iteration spins up its own fake TLS+gRPC
	// server (a handful of fds each) — the assertion is that fds don't
	// grow roughly linearly with the loop, which an unclosed conn per
	// enroll would cause.
	if after > before+40 {
		t.Fatalf("open fds grew from %d to %d over 20 enrollments, want it bounded (connections must be closed)", before, after)
	}
}

func openFDCountForTest(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("ReadDir(/proc/self/fd): %v", err)
	}
	return len(entries)
}
