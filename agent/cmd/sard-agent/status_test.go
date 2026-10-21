// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/agent-role.feature, tag @local: sard-agent status.

// statusHost is a host with a config and the tls files of a test.
type statusHost struct {
	t   *testing.T
	dir string
	cfg string
}

func newStatusHost(t *testing.T, address string) *statusHost {
	t.Helper()
	dir := t.TempDir()
	h := &statusHost{t: t, dir: dir}
	h.cfg = identityConfig(t, dir, address)
	return h
}

func (h *statusHost) cert() string { return filepath.Join(h.dir, "agent.pem") }
func (h *statusHost) key() string  { return filepath.Join(h.dir, "agent.key") }

// install writes a pair whose certificate has these SAN URIs.
func (h *statusHost) install(uris ...string) { installPair(h.t, h.dir, uris...) }

func (h *statusHost) status(args ...string) (code int, stdout, stderr string) {
	return runAgent(append([]string{"status"}, args...)...)
}

// requireRefusal checks code 1, empty stdout and one line of stderr with prefix.
func requireRefusal(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if code != 1 || stdout != "" || strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "sard-agent: ") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// Scenario: Статус зарегистрированного агента печатает агента, тенант, роль и срок сертификата
func TestStatusOfAnEnrolledAgentPrintsAgentTenantRoleAndExpiry(t *testing.T) {
	for _, role := range []string{"source", "verifier"} {
		h := newStatusHost(t, "127.0.0.1:1")
		h.install(withRole(role)...)
		code, stdout, stderr := h.status("--config", h.cfg)
		want := "agent_id: " + testAgent + "\ntenant_id: " + testTenant + "\nrole: " + role + "\ncertificate_not_after: 2027-10-12T12:00:00Z\n"
		// The four lines of С6 come first; since C1 the lines about the certificate follow.
		if code != 0 || !strings.HasPrefix(stdout, want) || stderr != "" {
			t.Errorf("%s: code = %d, stdout = %q, stderr = %q", role, code, stdout, stderr)
		}
	}
}

// Scenario: Статус находит агента и роль при URI роли перед URI личности
func TestStatusFindsTheAgentAndTheRoleWhenTheRoleURIComesFirst(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	h.install("sard://roles/verifier", testIdentityURI)
	code, stdout, _ := h.status("--config", h.cfg)
	for _, want := range []string{"agent_id: " + testAgent + "\n", "tenant_id: " + testTenant + "\n", "role: verifier\n"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Errorf("code = %d, stdout = %q lacks %q", code, stdout, want)
		}
	}
}

// Scenario: Статус не обращается к серверу
func TestStatusDoesNotTouchTheServer(t *testing.T) {
	addr, accepted := countingServer(t)
	h := newStatusHost(t, addr)
	h.install(withRole("source")...)
	if code, _, stderr := h.status("--config", h.cfg); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if accepted.Load() != 0 {
		t.Errorf("connections = %d", accepted.Load())
	}
}

// Scenario: Статус не читает ключ агента
func TestStatusDoesNotReadTheKey(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	h.install(withRole("source")...)
	if err := os.Remove(h.key()); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := h.status("--config", h.cfg); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

// Scenario: Статус печатает истёкший сертификат как есть
func TestStatusPrintsAnExpiredCertificateAsItIs(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	k := newKey(t)
	writeFile(t, h.cert(), certOf(t, k, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), withRole("source")...), 0o644)
	code, stdout, _ := h.status("--config", h.cfg)
	if code != 0 || !strings.Contains(stdout, "certificate_not_after: 2026-01-01T00:00:00Z\n") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

// Scenario: Статус незарегистрированного агента отказывает
func TestStatusOfAnAgentThatIsNotEnrolledRefuses(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	code, stdout, stderr := h.status("--config", h.cfg)
	if code != 1 || stdout != "" || stderr != "sard-agent: not enrolled: run sard-agent enroll\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// An agent config without tls paths is not enrolled either.
func TestStatusWithoutTLSPathsInTheConfigIsNotEnrolled(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: 127.0.0.1:1\n")
	code, _, stderr := runAgent("status", "--config", cfg)
	if code != 1 || stderr != "sard-agent: not enrolled: run sard-agent enroll\n" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

// Scenario: Статус с испорченным сертификатом называет ключ конфига и путь
func TestStatusOfABrokenCertificateNamesTheConfigKeyAndThePath(t *testing.T) {
	body := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("MARKER-BODY-0123456789")})
	rows := map[string]func(h *statusHost){
		"empty file":           func(h *statusHost) { writeFile(t, h.cert(), nil, 0o644) },
		"unparsable body":      func(h *statusHost) { writeFile(t, h.cert(), body, 0o644) },
		"role but no identity": func(h *statusHost) { h.install("sard://roles/source") },
		"certificate missing": func(h *statusHost) {
			if err := os.Remove(h.cert()); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range rows {
		h := newStatusHost(t, "127.0.0.1:1")
		h.install(withRole("source")...)
		setup(h)
		code, stdout, stderr := h.status("--config", h.cfg)
		requireRefusal(t, code, stdout, stderr)
		if !strings.Contains(stderr, "tls.cert_file") || !strings.Contains(stderr, h.cert()) {
			t.Errorf("%s: stderr = %q", name, stderr)
		}
		requireNoKeyMaterial(t, stdout, stderr)
		if strings.Contains(stderr, "MARKER") || strings.Contains(stderr, "TUFSS0VSLUJPRFk") {
			t.Errorf("%s: stderr = %q", name, stderr)
		}
	}
}

// Scenario: Статус с испорченным сертификатом (файл с правами 0000)
func TestStatusOfAnUnreadableCertificateNamesTheConfigKeyAndThePath(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	h.install(withRole("source")...)
	deps := realStatusDeps()
	deps.readFile = func(name string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	var out, errOut bytes.Buffer
	code := runStatus([]string{"--config", h.cfg}, &out, &errOut, deps)
	requireRefusal(t, code, out.String(), errOut.String())
	if !strings.Contains(errOut.String(), "tls.cert_file") || !strings.Contains(errOut.String(), h.cert()) {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// Scenario: Статус сертификата без допустимой роли отказывает с причиной
func TestStatusOfACertificateWithoutAValidRoleRefusesWithTheReason(t *testing.T) {
	for _, row := range roleRefusalRows {
		h := newStatusHost(t, "127.0.0.1:1")
		h.install(append([]string{testIdentityURI}, row.names...)...)
		code, stdout, stderr := h.status("--config", h.cfg)
		requireRefusal(t, code, stdout, stderr)
		if !strings.Contains(stderr, row.reason) || !strings.Contains(stderr, "sard-agent enroll --force") {
			t.Errorf("%v: stderr = %q", row.names, stderr)
		}
	}
}

// Scenario: Ошибка флагов статуса — код использования
func TestStatusFlagErrorsAreUsageErrors(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: 127.0.0.1:1\n")
	for _, args := range [][]string{{}, {"--config", cfg, "--unknown-flag"}, {"--config", cfg, "extra"}} {
		code, stdout, stderr := runAgent(append([]string{"status"}, args...)...)
		if code != 2 || stdout != "" || stderr == "" {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

// A config that cannot be loaded is a local error, like at start.
func TestStatusWithABrokenConfigIsALocalError(t *testing.T) {
	code, stdout, stderr := runAgent("status", "--config", filepath.Join(t.TempDir(), "missing.yaml"))
	requireRefusal(t, code, stdout, stderr)
}

// Scenario: Статус не печатает идентификатор машины
func TestStatusDoesNotPrintTheMachineID(t *testing.T) {
	useMachineIDFiles(t, machineIDFile(t, rawMachineID))
	h := newStatusHost(t, "127.0.0.1:1")
	h.install(withRole("source")...)
	_, stdout, stderr := h.status("--config", h.cfg)
	for _, out := range []string{stdout, stderr} {
		if strings.Contains(out, rawMachineID) || strings.Contains(out, machineIDOfR1) {
			t.Errorf("output holds a machine id: %q", out)
		}
	}
}

// A file that cannot even be examined is not "not enrolled": the read says why.
func TestStatusTreatsAFileItCannotExamineAsPresent(t *testing.T) {
	h := newStatusHost(t, "127.0.0.1:1")
	deps := statusDeps{
		readFile: func(name string) ([]byte, error) {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
		},
		stat: func(name string) (os.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrPermission}
		},
	}
	var out, errOut bytes.Buffer
	code := runStatus([]string{"--config", h.cfg}, &out, &errOut, deps)
	requireRefusal(t, code, out.String(), errOut.String())
	if !strings.Contains(errOut.String(), "tls.cert_file") {
		t.Errorf("stderr = %q", errOut.String())
	}
}
