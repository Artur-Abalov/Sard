// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-start-refusals.feature (OQ-001, OQ-050), tag @start.

const refusalSuffix = "not reconnecting: fix the cause, then restart the agent"

// tlsServer is AgentService over TLS whose Register answers as the test says.
type tlsServer struct {
	agentv1.UnimplementedAgentServiceServer
	reply    func(ctx context.Context) error
	registry atomic.Int32
	connects atomic.Int32
	// last is the latest RegisterRequest.
	last atomic.Pointer[agentv1.RegisterRequest]
}

func (s *tlsServer) Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	s.registry.Add(1)
	s.last.Store(req)
	return nil, s.reply(ctx)
}

func (s *tlsServer) Connect(grpc.BidiStreamingServer[agentv1.ConnectRequest, agentv1.ConnectResponse]) error {
	s.connects.Add(1)
	return status.Error(codes.Unavailable, "no")
}

// refusalHost is an agent host whose server trusts a test CA.
type refusalHost struct {
	t      *testing.T
	dir    string
	server *tlsServer
	cfg    string
	restic string
	// address is where the server listens.
	address string
}

func newRefusalHost(t *testing.T, reply func(ctx context.Context) error) *refusalHost {
	t.Helper()
	dir := t.TempDir()
	writeIdentity(t, dir)
	ca := newTestCA(t)
	cert := ca.leaf(t, []string{"127.0.0.1"}, 0)
	writeFile(t, filepath.Join(dir, "ca.pem"), []byte(ca.pem()), 0o600)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &tlsServer{reply: reply}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
	agentv1.RegisterAgentServiceServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	h := &refusalHost{t: t, dir: dir, server: srv, restic: withRestic(t)}
	h.address = lis.Addr().String()
	h.cfg = h.configFor(h.address)
	return h
}

// configFor is the agent config for a server at address.
func (h *refusalHost) configFor(address string) string {
	return writeConfig(h.t, "server:\n  address: "+address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\n"+h.restic)
}

// runAgent starts the agent and stops it after limit; it returns early when the agent exits.
func (h *refusalHost) run(limit time.Duration) (code int, stdout, stderr string) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	var out, errOut bytes.Buffer
	code = run(ctx, []string{"--config", h.cfg}, &out, &errOut, fixedHostname)
	return code, out.String(), errOut.String()
}

func refusedWith(code codes.Code, msg, domain, reason string, metadata map[string]string) func(context.Context) error {
	return func(context.Context) error {
		st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{Domain: domain, Reason: reason, Metadata: metadata})
		if err != nil {
			panic(err)
		}
		return st.Err()
	}
}

// requireRefusal checks exit code 78, one Register and no Connect, and one
// refusal line that contains wants and ends with the suffix.
func (h *refusalHost) requireRefusal(limit time.Duration, wants []string, unwanted ...string) {
	h.t.Helper()
	code, _, stderr := h.run(limit)
	line := strings.TrimSuffix(stderr, "\n")
	if code != 78 || h.server.registry.Load() != 1 || h.server.connects.Load() != 0 {
		h.t.Fatalf("exit code = %d, Register = %d, Connect = %d, stderr = %q", code, h.server.registry.Load(), h.server.connects.Load(), stderr)
	}
	if strings.Contains(line, "\n") || !strings.HasPrefix(line, "sard-agent: ") || !strings.HasSuffix(line, refusalSuffix) {
		h.t.Errorf("line = %q", line)
	}
	requireContains(h.t, line, wants, true)
	requireContains(h.t, line, unwanted, false)
}

// requireContains checks that every part is (or, with present false, is not) in line.
func requireContains(t *testing.T, line string, parts []string, present bool) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(line, part) != present {
			t.Errorf("contains %q = %v, want %v: %q", part, !present, present, line)
		}
	}
}

// Scenario: Отказ Register с причиной сервера завершает агента с кодом 78
func TestRegisterRefusalWithAServerReasonEndsTheAgentWithCode78(t *testing.T) {
	rows := []struct {
		code     codes.Code
		reason   string
		metadata map[string]string
		shown    string
	}{
		{codes.InvalidArgument, "HOSTNAME_INVALID", map[string]string{"field": "hostname"}, "reason HOSTNAME_INVALID, field=hostname"},
		{codes.InvalidArgument, "SNAPSHOT_TOO_LARGE", map[string]string{"limit": "256", "field": "repositories"}, "reason SNAPSHOT_TOO_LARGE, field=repositories, limit=256"},
		{codes.FailedPrecondition, "PROTOCOL_UNSUPPORTED", map[string]string{"min_supported": "2", "max_supported": "3"}, "reason PROTOCOL_UNSUPPORTED, max_supported=3, min_supported=2"},
	}
	for _, row := range rows {
		h := newRefusalHost(t, refusedWith(row.code, "register rejected", "sard.dev", row.reason, row.metadata))
		h.requireRefusal(10*time.Second, []string{"code = " + row.code.String(), "desc = register rejected", row.shown})
	}
}

// Scenario: Отказ Register без ErrorInfo тоже завершает агента с кодом 78
func TestRegisterRefusalWithoutErrorInfoAlsoEndsTheAgentWithCode78(t *testing.T) {
	for _, c := range []codes.Code{codes.InvalidArgument, codes.FailedPrecondition, codes.Unauthenticated, codes.PermissionDenied} {
		h := newRefusalHost(t, func(context.Context) error { return status.Error(c, "refused-marker") })
		h.requireRefusal(10*time.Second, []string{"code = " + c.String(), "refused-marker"}, "reason ")
	}
}

// Scenario: ErrorInfo чужого домена не выдаётся за причину сервера
func TestErrorInfoOfAnotherDomainIsNotShownAsTheServersReason(t *testing.T) {
	h := newRefusalHost(t, refusedWith(codes.InvalidArgument, "register rejected", "other.example", "NAME_DUPLICATE", nil))
	code, _, stderr := h.run(10 * time.Second)
	if code != 78 || strings.Contains(stderr, "NAME_DUPLICATE") {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
}

// Scenario: Управляющие символы в ответе сервера не разрывают строку отказа
func TestControlCharactersFromTheServerDoNotBreakTheRefusalLine(t *testing.T) {
	h := newRefusalHost(t, refusedWith(codes.InvalidArgument, "register rejected", "sard.dev", "BAD\nREASON", map[string]string{"field": "x\ny"}))
	code, _, stderr := h.run(10 * time.Second)
	if code != 78 || strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, `reason BAD\nREASON, field=x\ny`) {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
}

// Scenario: Остановка агента во время отказа Register завершает его с кодом 0
func TestStoppingTheAgentDuringARegisterRefusalEndsItWithCode0(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	h := newRefusalHost(t, func(context.Context) error {
		stop() // SIGTERM arrives while Register is in flight
		return status.Error(codes.InvalidArgument, "register rejected")
	})
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"--config", h.cfg}, &out, &errOut, fixedHostname)
	if code != 0 || strings.Contains(errOut.String(), refusalSuffix) {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
}

// Scenario: Недоступный сервер не останавливает агента
func TestAnUnreachableServerDoesNotStopTheAgent(t *testing.T) {
	h := newRefusalHost(t, nil)
	h.cfg = h.configFor("127.0.0.1:1")
	code, _, stderr := h.run(500 * time.Millisecond)
	if code != 0 || strings.Contains(stderr, refusalSuffix) {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
}

// serviceSetting returns the value of key in the [Service] section of the unit.
func serviceSetting(t *testing.T, key string) string {
	t.Helper()
	data, err := os.ReadFile("../../../deploy/agent/sard-agent.service")
	if err != nil {
		t.Fatal(err)
	}
	inService := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "[") {
			inService = strings.TrimSpace(line) == "[Service]"
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && inService && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Scenario: Юнит службы запрещает перезапуск после кода 78
func TestTheUnitDoesNotRestartTheAgentAfterCode78(t *testing.T) {
	if got := serviceSetting(t, "RestartPreventExitStatus"); got != strconv.Itoa(exitRefused) || exitRefused != 78 {
		t.Errorf("RestartPreventExitStatus = %q", got)
	}
	if got := serviceSetting(t, "Restart"); got != "on-failure" {
		t.Errorf("Restart = %q", got)
	}
}
