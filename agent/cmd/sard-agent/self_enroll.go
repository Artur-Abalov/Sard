// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The --enroll-token-file step of "sard-agent --config ..." (F5 phase 3,
// docs/specs/agent/self-agent.feature): before the usual start checks the
// agent looks for an enrollment token another process left in a file, and
// registers itself with the existing enroll pipeline (enroll_run.go), or
// waits for the file when it has neither a token nor an identity.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

const (
	// selfEnrollPollInterval is how often a waiting agent looks for the file (Р, E).
	selfEnrollPollInterval = 2 * time.Second
	// tokenMarkerSuffix is appended to tls.cert_file to name the marker (Р3).
	tokenMarkerSuffix = ".token-sha256"
	tokenMarkerMode   = 0o600
)

// selfEnrollDeps is everything the step reaches outside its arguments, so
// tests can drive the polling clock, the files and the enroll pipeline.
type selfEnrollDeps struct {
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, perm os.FileMode) error
	clock     clock
	inspect   func(config.TLS) (enroll.IdentityStatus, error)
	enroll    enrollDeps
}

func realSelfEnrollDeps(hostname hostnameFunc) selfEnrollDeps {
	return selfEnrollDeps{
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		clock:     realEnrollClock{},
		inspect:   enroll.InspectIdentity,
		enroll:    productionEnrollDeps(hostname),
	}
}

type selfOutcomeKind int

const (
	outcomeProceed selfOutcomeKind = iota
	outcomeWait
	outcomeExit
)

type selfOutcome struct {
	kind selfOutcomeKind
	code int
}

type selfEnroller struct {
	configPath string
	tokenPath  string
	cfg        config.Config
	stdout     io.Writer
	stderr     io.Writer
	deps       selfEnrollDeps
}

// selfEnrollIfAsked is the step behind the flag; without the flag the agent
// starts exactly as before.
func selfEnrollIfAsked(ctx context.Context, configPath, tokenPath string, stdout, stderr io.Writer, deps selfEnrollDeps) (bool, int) {
	if tokenPath == "" {
		return true, exitOK
	}
	return selfEnrollStep(ctx, configPath, tokenPath, stdout, stderr, deps)
}

// selfEnrollStep reports whether the normal start should go on; when it
// should not, code is the exit code (0 after SIGTERM while waiting).
func selfEnrollStep(ctx context.Context, configPath, tokenPath string, stdout, stderr io.Writer, deps selfEnrollDeps) (bool, int) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return false, reportFailure(stderr, err)
	}
	if cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "" {
		// Nothing to register into: the usual start names the missing key.
		return true, exitOK
	}
	s := &selfEnroller{configPath: configPath, tokenPath: tokenPath, cfg: cfg, stdout: stdout, stderr: stderr, deps: deps}
	return s.run(ctx)
}

func (s *selfEnroller) run(ctx context.Context) (bool, int) {
	for warned := false; ; warned = true {
		o := s.attempt(ctx)
		if o.kind != outcomeWait {
			return o.kind == outcomeProceed, o.code
		}
		if !warned {
			_, _ = fmt.Fprintf(s.stderr, "sard-agent: waiting for the enrollment token in %s\n", s.tokenPath)
		}
		select {
		case <-ctx.Done():
			return false, exitOK
		case <-s.deps.clock.After(selfEnrollPollInterval):
		}
	}
}

func (s *selfEnroller) attempt(ctx context.Context) selfOutcome {
	data, err := s.deps.readFile(s.tokenPath)
	if errors.Is(err, fs.ErrNotExist) {
		return s.idle()
	}
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "sard-agent: reading the enrollment token file %s: %v\n", s.tokenPath, err)
		return selfOutcome{outcomeExit, exitUsage}
	}
	raw := enroll.NormalizeTokenFile(data)
	if raw == "" {
		_, _ = fmt.Fprintf(s.stderr, "sard-agent: the enrollment token file %s is empty\n", s.tokenPath)
		return selfOutcome{outcomeExit, exitUsage}
	}
	if s.spent(raw) {
		return s.idle()
	}
	return s.enrollWith(ctx, raw)
}

// idle is the outcome when there is no token to use: start with an
// identity, wait without one.
func (s *selfEnroller) idle() selfOutcome {
	status, err := enroll.InspectIdentity(s.cfg.TLS)
	if err != nil {
		return selfOutcome{outcomeExit, reportIdentityInspectionError(s.stderr, err)}
	}
	if status.Exists {
		return selfOutcome{outcomeProceed, exitOK}
	}
	return selfOutcome{outcomeWait, exitOK}
}

func (s *selfEnroller) markerPath() string { return s.cfg.TLS.CertFile + tokenMarkerSuffix }

func tokenDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// spent: the marker holds the digest of this very token (Р3).
func (s *selfEnroller) spent(raw string) bool {
	marker, err := s.deps.readFile(s.markerPath())
	return err == nil && string(marker) == tokenDigest(raw)
}

// enrollWith runs the enroll pipeline in this process, replacing an
// existing identity, then records the token as spent.
func (s *selfEnroller) enrollWith(ctx context.Context, raw string) selfOutcome {
	opts := enrollOptions{token: raw, tokenSet: true, force: true, timeout: defaultEnrollTimeout, configPath: s.configPath}
	// The running agent enrolls as itself: it neither creates directories nor
	// hands files to another user, as enroll does only under root (A8a, Р25).
	self := hostsetup.Principal{Role: hostsetup.RoleService}
	if code := doEnroll(ctx, opts, self, io.Discard, s.stderr, s.deps.enroll); code != exitOK {
		return selfOutcome{outcomeExit, code}
	}
	status, err := s.deps.inspect(s.cfg.TLS)
	if err != nil || status.AgentID == "" {
		_, _ = fmt.Fprintf(s.stderr, "sard-agent: enrolled but the agent id could not be read from %s: %v\n", s.cfg.TLS.CertFile, err)
		return selfOutcome{outcomeExit, exitAgentError}
	}
	if err := s.deps.writeFile(s.markerPath(), []byte(tokenDigest(raw)), tokenMarkerMode); err != nil {
		_, _ = fmt.Fprintf(s.stderr, "sard-agent: enrolled as agent %s but writing the token marker %s failed: %v\n", status.AgentID, s.markerPath(), err)
		return selfOutcome{outcomeExit, exitWrite}
	}
	_, _ = fmt.Fprintf(s.stdout, "sard-agent: enrolled as agent %s\n", status.AgentID)
	return selfOutcome{outcomeProceed, exitOK}
}
