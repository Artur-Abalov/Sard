// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The healthy host of the conventions of docs/specs/agent/host-check.feature
// for sard-agent check (A8d-2): the repository base, the secret pg, a
// certificate that holds for 200 days, systemd with the service running, the
// PostgreSQL tools 16.4, SELinux not enforcing.

// progAnswer is what a program of the host prints and how it ends.
type progAnswer struct {
	out  string
	code int
	err  error
}

// progCall is one run of a program of the host.
type progCall struct {
	key   string
	runAs *restic.RunAs
	env   []string
}

// fakePrograms are the programs sard-agent check runs besides restic:
// getenforce, matchpathcon, ausearch, systemctl, the tools of PostgreSQL. A
// program that has no answer is not installed.
type fakePrograms struct {
	mu      sync.Mutex
	answers map[string]progAnswer
	// prefixes answer the runs whose key starts with one of them and that
	// have no answer of their own.
	prefixes map[string]progAnswer
	calls    []progCall
}

func (p *fakePrograms) Run(_ context.Context, cmd restic.Command) (int, error) {
	key := strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")
	p.mu.Lock()
	p.calls = append(p.calls, progCall{key: key, runAs: cmd.RunAs, env: cmd.Env})
	a, ok := p.answers[key]
	for prefix, answer := range p.prefixes {
		if !ok && strings.HasPrefix(key, prefix) {
			a, ok = answer, true
		}
	}
	p.mu.Unlock()
	if !ok {
		return -1, &fs.PathError{Op: "fork/exec", Path: cmd.Path, Err: syscall.ENOENT}
	}
	if a.err != nil {
		return -1, a.err
	}
	if cmd.Stdout != nil {
		for l := range strings.Lines(a.out) {
			cmd.Stdout([]byte(strings.TrimSuffix(l, "\n")))
		}
	}
	return a.code, nil
}

func (p *fakePrograms) answer(key, out string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[key] = progAnswer{out: out}
}

// prefix answers every run whose key starts with the text.
func (p *fakePrograms) prefix(prefix, out string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prefixes == nil {
		p.prefixes = map[string]progAnswer{}
	}
	p.prefixes[prefix] = progAnswer{out: out}
}

// removePrefix takes the answers away that were given for the prefix.
func (p *fakePrograms) removePrefix(prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.prefixes {
		if strings.HasPrefix(k, prefix) {
			delete(p.prefixes, k)
		}
	}
}

func (p *fakePrograms) remove(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.answers, key)
}

// ran says how often the program was run with the arguments (a prefix of the key).
func (p *fakePrograms) ran(prefix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		if strings.HasPrefix(c.key, prefix) {
			n++
		}
	}
	return n
}

func (p *fakePrograms) keys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var keys []string
	for _, c := range p.calls {
		keys = append(keys, c.key)
	}
	return keys
}

const (
	pgBin      = "/usr/bin"
	showActive = "ActiveState=active\nSubState=running\nNRestarts=0\nActiveEnterTimestamp=Wed 2026-10-14 08:00:00 UTC\n"
	showKey    = "systemctl show sard-agent.service -p ActiveState -p SubState -p NRestarts -p ActiveEnterTimestamp"
)

func pgVersionLine(tool, version string) string {
	return tool + " (PostgreSQL) " + version
}

// installPostgres puts the tools of PostgreSQL of the version into dir.
func (h *setupHost) installPostgres(dir, version string) {
	for _, tool := range []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"} {
		h.progs.answer(dir+"/"+tool+" --version", pgVersionLine(tool, version))
	}
}

// pki is a fake CA, a certificate it issued and the key of the certificate.
type testPKI struct{ ca, cert, key []byte }

func newTestPKI(t *testing.T, notBefore, notAfter time.Time) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ok(t, err)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: notBefore.Add(-time.Hour), NotAfter: notAfter.Add(1000 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ok(t, err)
	ca, err := x509.ParseCertificate(caDER)
	ok(t, err)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ok(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "agent"}, NotBefore: notBefore, NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	ok(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	ok(t, err)
	return testPKI{
		ca:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
}

// tlsDir is D/tls, where enroll puts the identity.
func (h *setupHost) tlsDir() string { return h.path("tls") }

// installIdentity writes the certificate, its key and the CA of p into D/tls.
func (h *setupHost) installIdentity(p testPKI) {
	h.t.Helper()
	ok(h.t, os.MkdirAll(h.tlsDir(), 0o700))
	ok(h.t, os.Chmod(h.tlsDir(), 0o700))
	h.write(h.cfg.TLS.CAFile, string(p.ca), 0o644)
	h.write(h.cfg.TLS.CertFile, string(p.cert), 0o644)
	h.write(h.cfg.TLS.KeyFile, string(p.key), 0o600)
}

// certHolding is a certificate that is valid for the time from the clock on.
func (h *setupHost) certHolding(left time.Duration) testPKI {
	return newTestPKI(h.t, h.clock.now.Add(-24*time.Hour), h.clock.now.Add(left))
}

// certEnding is a certificate of a year that ends after left.
func (h *setupHost) certEnding(left time.Duration) testPKI {
	return newTestPKI(h.t, h.clock.now.Add(left-365*24*time.Hour), h.clock.now.Add(left))
}

// fullHost is the healthy host: everything sard-agent check looks at is as it
// should be.
func fullHost(t *testing.T) *setupHost {
	t.Helper()
	h := checkHost(t)
	h.deps.pathEnv = "/usr/local/bin:" + pgBin
	h.installPostgres(pgBin, "16.4")
	h.progs.answer("getenforce", "Permissive\n")
	h.progs.answer(showKey, showActive)
	h.write(h.cfg.Restic.Path, "#!/bin/sh\n", 0o755)
	h.installIdentity(h.certHolding(200 * 24 * time.Hour))
	ok(t, os.Chmod(h.secretsDir(), 0o700))
	ok(t, os.MkdirAll(h.agentD(), 0o750))
	ok(t, os.Chmod(h.agentD(), 0o750))
	ok(t, os.Chmod(h.cfgPath, 0o640))
	ok(t, os.MkdirAll(filepath.Join(h.stateDir(), "journal"), 0o700))
	h.rootOwns(h.cfgPath, h.agentD())
	return h
}

// rootOwns makes the files root's, readable by the group of the service user (Р3).
func (h *setupHost) rootOwns(paths ...string) {
	for _, p := range paths {
		h.fsys.setOwner(p, 0, serviceUID)
	}
}

// check is `sard-agent check` as root.
func (h *setupHost) check(extra ...string) (int, string, string) {
	return h.sudo(append([]string{"check", "--config", "C"}, extra...)...)
}

// writeFragment writes a fragment of agent.d, root's, as repo add makes it.
func (h *setupHost) writeFragment(name, content string, mode os.FileMode) {
	h.t.Helper()
	path := filepath.Join(h.agentD(), name)
	h.write(path, content, mode)
	h.rootOwns(path)
}

// checkLines runs check and returns the lines it printed.
func (h *setupHost) checkLines(extra ...string) (lines []outLine, summary string, code int) {
	h.t.Helper()
	code, stdout, stderr := h.check(extra...)
	if stdout == "" {
		h.t.Fatalf("code %d, no output, stderr %q", code, stderr)
	}
	lines, summary = parseOut(h.t, strings.ReplaceAll(stdout, "some checks need root: run sudo sard-agent check\n", ""))
	return lines, summary, code
}

// wantIDs are the identifiers of the checks of a healthy host with one
// repository and one secret, in the order of Ч10.
var wantIDs = []string{
	"service.user", "config.load", "config.names", "config.references", "files.permissions", "restic.binary",
	"tls.identity", "tls.chain", "tls.expiry", "tls.role",
	"server.reachable", "server.tls", "server.accepted", "clock.skew",
	"repository.storage", "repository.credentials", "repository.initialized", "repository.password", "repository.id", "repository.keys", "repository.locks",
	"secret.readable", "plugin.declared", "plugin.programs", "plugin.programs", "restore.locations",
	"selinux.labels", "selinux.denials", "service.running", "service.steps",
}

// fsPathError is an error of the file system, as exec reports it.
type fsPathError struct {
	path string
	err  error
}

func (e *fsPathError) Error() string { return e.path + ": " + e.err.Error() }
func (e *fsPathError) Unwrap() error { return e.err }

// saveHostConfig writes the config as the host has it: root's, 0640.
func (h *setupHost) saveHostConfig() {
	h.t.Helper()
	h.saveConfig()
	ok(h.t, os.Chmod(h.cfgPath, 0o640))
	h.rootOwns(h.cfgPath)
}
