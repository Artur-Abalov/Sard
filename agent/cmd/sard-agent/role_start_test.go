// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-role.feature, tags @start and @transport.

// roleServer is AgentService over TLS that accepts Register, counts what
// arrives and, on a stream, sends the steps the test queued after Hello.
type roleServer struct {
	agentv1.UnimplementedAgentServiceServer
	mu        sync.Mutex
	registers []*agentv1.RegisterRequest
	hellos    []*agentv1.Hello
	results   []*agentv1.StepResult
	steps     []*agentv1.RunStep
	// dropStream makes the server end every stream right after Hello.
	dropStream bool
	onRegister func()
	changed    chan struct{}
}

func (s *roleServer) Register(_ context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	s.mu.Lock()
	s.registers = append(s.registers, req)
	s.mu.Unlock()
	s.notify()
	if s.onRegister != nil {
		s.onRegister()
	}
	return &agentv1.RegisterResponse{HeartbeatInterval: durationpb.New(time.Hour)}, nil
}

func (s *roleServer) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *roleServer) Connect(stream grpc.BidiStreamingServer[agentv1.ConnectRequest, agentv1.ConnectResponse]) error {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.hellos = append(s.hellos, msg.GetHello())
	steps, drop := s.steps, s.dropStream
	s.mu.Unlock()
	s.notify()
	if drop {
		return status.Error(codes.Unavailable, "dropped by the test")
	}
	for _, step := range steps {
		if err := stream.Send(&agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_RunStep{RunStep: step}}); err != nil {
			return err
		}
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			return nil
		}
		if r := msg.GetStepResult(); r != nil {
			s.mu.Lock()
			s.results = append(s.results, r)
			s.mu.Unlock()
			s.notify()
		}
	}
}

func (s *roleServer) registerRequests() []*agentv1.RegisterRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*agentv1.RegisterRequest(nil), s.registers...)
}

func (s *roleServer) stepResults() []*agentv1.StepResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*agentv1.StepResult(nil), s.results...)
}

// agentHost is an agent host whose server is a roleServer of a test CA.
type agentHost struct {
	t       *testing.T
	dir     string
	server  *roleServer
	address string
	// restic is the YAML of the restic section; resticCalls the file in
	// which the fake restic notes every invocation.
	restic      string
	resticCalls string
	extra       string
	// stateDir is executor.state_dir.
	stateDir string
	log      *lockedBuffer
}

func newAgentHost(t *testing.T) *agentHost {
	t.Helper()
	dir := t.TempDir()
	writeIdentity(t, dir)
	ca := newTestCA(t)
	writeFile(t, filepath.Join(dir, "ca.pem"), []byte(ca.pem()), 0o600)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &roleServer{changed: make(chan struct{}, 1000)}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{ca.leaf(t, []string{"127.0.0.1"}, 0)}, MinVersion: tls.VersionTLS12})))
	agentv1.RegisterAgentServiceServer(g, srv)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	h := &agentHost{t: t, dir: dir, server: srv, address: lis.Addr().String(), log: &lockedBuffer{}, stateDir: filepath.Join(dir, "state")}
	h.resticCalls = filepath.Join(dir, "restic-calls")
	script := "#!/bin/sh\necho \"$1\" >> " + h.resticCalls + "\necho 'restic 0.19.1 compiled with go1.26.4 on linux/amd64'\n"
	h.restic = "restic: {path: " + writeScript(t, filepath.Join(dir, "restic"), script) + "}\n"
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(h.log, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return h
}

func writeScript(t *testing.T, path, body string) string {
	t.Helper()
	writeFile(t, path, []byte(body), 0o755)
	return path
}

func (h *agentHost) config() string {
	return writeConfig(h.t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.stateDir+"}\n"+h.restic+h.extra)
}

func (h *agentHost) certPath() string { return filepath.Join(h.dir, "agent.pem") }

// runUntil starts the agent and stops it once done reports true (polled on
// every event of the server) or after limit.
func (h *agentHost) runUntil(limit time.Duration, args []string, done func() bool) (code int, stdout, stderr string) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			if done != nil && done() {
				cancel()
				return
			}
			select {
			case <-h.server.changed:
			case <-time.After(20 * time.Millisecond):
			case <-stopped:
				return
			}
		}
	}()
	var out, errOut bytes.Buffer
	code = run(ctx, args, &out, &errOut, fixedHostname)
	return code, out.String(), errOut.String()
}

// registered runs the agent until it has sent n Register requests.
func (h *agentHost) registered(n int) (code int, stdout, stderr string) {
	h.t.Helper()
	return h.runUntil(10*time.Second, []string{"--config", h.config()}, func() bool { return len(h.server.registerRequests()) >= n })
}

// refuses runs the agent, which must stop with code 1 and one line of
// stderr without ever connecting.
func (h *agentHost) refuses() (stdout, stderr string) {
	h.t.Helper()
	code, stdout, stderr := h.runUntil(5*time.Second, []string{"--config", h.config()}, nil)
	if code != 1 || strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "sard-agent: ") {
		h.t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "connecting to") || len(h.server.registerRequests()) != 0 {
		h.t.Errorf("stdout = %q, Register = %d", stdout, len(h.server.registerRequests()))
	}
	return stdout, stderr
}

func fileContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Scenario: Строка старта называет роль из сертификата
func TestTheStartLineNamesTheRoleFromTheCertificate(t *testing.T) {
	for _, role := range []string{"source", "verifier"} {
		h := newAgentHost(t)
		installPair(t, h.dir, withRole(role)...)
		code, stdout, stderr := h.registered(1)
		want := "sard-agent dev: connecting to " + h.address + " as " + role + "\n"
		if code != 0 || stdout != want {
			t.Errorf("%s: code = %d, stdout = %q, stderr = %q", role, code, stdout, stderr)
		}
	}
}

// Scenario: Сертификат без допустимой роли останавливает агента до подключения
func TestACertificateWithoutAValidRoleStopsTheAgentBeforeConnecting(t *testing.T) {
	for _, row := range roleRefusalRows {
		h := newAgentHost(t)
		installPair(t, h.dir, append([]string{testIdentityURI}, row.names...)...)
		_, stderr := h.refuses()
		for _, want := range []string{row.reason, "tls.cert_file", h.certPath(), "sard-agent enroll --force"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v: %q lacks %q", row.names, stderr, want)
			}
		}
	}
}

// Scenario: URI роли из сертификата не попадает в строку отказа
func TestTheRoleURIOfTheCertificateIsNotPrinted(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, testIdentityURI, "sard://roles/marker-ROLE-7f")
	stdout, stderr := h.refuses()
	if strings.Contains(stdout+stderr, "marker-ROLE-7f") {
		t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
	}
}

// Scenario: Агент с URI роли перед URI личности стартует с этой ролью
func TestARoleURIBeforeTheIdentityURIStartsWithThatRole(t *testing.T) {
	h := newAgentHost(t)
	useMachineIDFiles(t, machineIDFile(t, rawMachineID+"\n"))
	installPair(t, h.dir, "sard://roles/verifier", testIdentityURI)
	code, stdout, stderr := h.registered(1)
	if code != 0 || !strings.Contains(stdout, " as verifier") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := h.server.registerRequests()[0].GetMachineId(); got != machineIDOfR1 {
		t.Errorf("machine_id = %q", got)
	}
}

// Scenario: Роль читается из первого сертификата файла
func TestTheRoleIsReadFromTheFirstCertificateOfTheFile(t *testing.T) {
	h := newAgentHost(t)
	k := newKey(t)
	first := certOf(t, k, roleCertNotAfter, testIdentityURI)
	second := certOf(t, newKey(t), roleCertNotAfter, withRole("source")...)
	writeFile(t, h.certPath(), append(first, second...), 0o644)
	writeFile(t, filepath.Join(h.dir, "agent.key"), keyPEM(t, k), 0o600)
	_, stderr := h.refuses()
	if !strings.Contains(stderr, "CERT_ROLE_MISSING") {
		t.Errorf("stderr = %q", stderr)
	}
}

// Scenario: Несовпадение ключа и сертификата сообщается раньше отсутствия роли
func TestAKeyMismatchIsReportedBeforeAMissingRole(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, testIdentityURI)
	other, _ := identityPEM(t)
	writeFile(t, filepath.Join(h.dir, "agent.key"), other, 0o600)
	_, stderr := h.refuses()
	if !strings.Contains(stderr, incompleteEnrollment) || strings.Contains(stderr, "CERT_ROLE_MISSING") {
		t.Errorf("stderr = %q", stderr)
	}
}

// Scenario: Отсутствие роли сообщается раньше проблемы с restic
func TestAMissingRoleIsReportedBeforeAResticProblem(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, testIdentityURI)
	h.restic = "restic: {path: " + h.dir + "/nowhere/restic}\n"
	_, stderr := h.refuses()
	if !strings.Contains(stderr, "CERT_ROLE_MISSING") || strings.Contains(stderr, "RESTIC_NOT_FOUND") {
		t.Errorf("stderr = %q", stderr)
	}
}

// Scenario: Отсутствие роли сообщается раньше проблемы с идентификатором машины
func TestAMissingRoleIsReportedBeforeAMachineIDProblem(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, testIdentityURI)
	dir := t.TempDir()
	useMachineIDFiles(t, dir)
	_, stderr := h.refuses()
	if !strings.Contains(stderr, "CERT_ROLE_MISSING") || strings.Contains(stderr, dir) {
		t.Errorf("stderr = %q", stderr)
	}
}

// Scenario: machine_id одинаков в Register до и после перезапуска агента
func TestTheMachineIDIsTheSameBeforeAndAfterARestart(t *testing.T) {
	h := newAgentHost(t)
	var ids []string
	for i := 1; i <= 2; i++ {
		if code, _, stderr := h.registered(i); code != 0 {
			t.Fatalf("run %d: code = %d, stderr = %q", i, code, stderr)
		}
		ids = append(ids, h.server.registerRequests()[i-1].GetMachineId())
	}
	if ids[0] == "" || ids[0] != ids[1] {
		t.Errorf("machine ids = %q", ids)
	}
	if got := fileContent(t, filepath.Join(h.dir, "state", "machine-id")); len(got) != 64 {
		t.Errorf("stored id = %q", got)
	}
}

// Scenario: Непригодный источник идентификатора останавливает агента до подключения
func TestAnUnusableMachineIDSourceStopsTheAgentBeforeConnecting(t *testing.T) {
	t.Run("a directory in place of the file", func(t *testing.T) {
		h := newAgentHost(t)
		dir := t.TempDir()
		useMachineIDFiles(t, dir)
		_, stderr := h.refuses()
		if !strings.Contains(stderr, dir) {
			t.Errorf("stderr = %q", stderr)
		}
	})
	stored := func(h *agentHost) string { return filepath.Join(h.dir, "state", "machine-id") }
	for name, content := range map[string]string{"not hex": "not-hex", "empty": ""} {
		t.Run("stored id "+name, func(t *testing.T) {
			h := newAgentHost(t)
			if err := os.MkdirAll(filepath.Join(h.dir, "state"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stored(h), []byte(content), 0o600)
			_, stderr := h.refuses()
			if !strings.Contains(stderr, stored(h)) {
				t.Errorf("stderr = %q", stderr)
			}
			// Scenario: Испорченный сохранённый идентификатор не перезаписывается
			if got := fileContent(t, stored(h)); got != content {
				t.Errorf("the stored id was replaced by %q", got)
			}
		})
	}
	t.Run("the state dir cannot be written", func(t *testing.T) {
		h := newAgentHost(t)
		file := filepath.Join(h.dir, "file")
		writeFile(t, file, []byte("x"), 0o600)
		h.stateDir = file + "/state"
		_, stderr := h.refuses()
		if !strings.Contains(stderr, file+"/state/machine-id") {
			t.Errorf("stderr = %q", stderr)
		}
	})
}

// Scenario: Исходный идентификатор и machine_id не попадают в вывод и журнал агента
func TestNeitherTheRawIDNorTheMachineIDReachesTheOutputOrTheLog(t *testing.T) {
	h := newAgentHost(t)
	useMachineIDFiles(t, machineIDFile(t, rawMachineID))
	code, stdout, stderr := h.registered(1)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	id := h.server.registerRequests()[0].GetMachineId()
	for _, out := range []string{stdout, stderr, h.log.String()} {
		if id == "" || strings.Contains(out, rawMachineID) || strings.Contains(out, id) {
			t.Errorf("output %q holds an id (machine_id = %q)", out, id)
		}
	}
}

// Scenario: Переподключение несёт тот же machine_id
func TestAReconnectCarriesTheSameMachineID(t *testing.T) {
	h := newAgentHost(t)
	h.server.dropStream = true
	code, _, stderr := h.registered(2)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	regs := h.server.registerRequests()
	if regs[0].GetMachineId() == "" || regs[0].GetMachineId() != regs[1].GetMachineId() {
		t.Errorf("machine ids = %q, %q", regs[0].GetMachineId(), regs[1].GetMachineId())
	}
}

// Scenario: Агент не сообщает роль серверу
func TestTheAgentDoesNotTellTheServerItsRole(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("verifier")...)
	code, _, stderr := h.runUntil(10*time.Second, []string{"--config", h.config()}, func() bool {
		h.server.mu.Lock()
		defer h.server.mu.Unlock()
		return len(h.server.hellos) > 0
	})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	for _, msg := range [][]byte{marshal(t, h.server.registers[0]), marshal(t, h.server.hellos[0])} {
		if bytes.Contains(msg, []byte("verifier")) {
			t.Errorf("a message holds the role: %q", msg)
		}
	}
}

// Scenario: Подставной сервер не может заставить верификатор сделать бэкап
func TestAForgedServerCannotMakeAVerifierBackUp(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("verifier")...)
	h.extra = "repositories:\n  - {name: qa, url: " + h.dir + "/repo, password_file: " + h.dir + "/qa.pass}\n"
	writeFile(t, filepath.Join(h.dir, "qa.pass"), []byte("pw"), 0o600)
	h.server.steps = []*agentv1.RunStep{{CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "qa"}}
	code, _, stderr := h.runUntil(10*time.Second, []string{"--config", h.config()}, func() bool { return len(h.server.stepResults()) > 0 })
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	r := h.server.stepResults()[0]
	if r.GetCommandId() != "c1" || r.GetStatus() != agentv1.StepStatus_STEP_STATUS_REJECTED || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_ROLE_MISMATCH {
		t.Fatalf("result = %v", r)
	}
	h.requireNoStepWork()
}

// requireNoStepWork: restic ran only for the start's version check and the
// repository id of Register ("cat"), and the log has no work of c1.
func (h *agentHost) requireNoStepWork() {
	h.t.Helper()
	for _, call := range strings.Fields(fileContent(h.t, h.resticCalls)) {
		if call != "version" && call != "cat" {
			h.t.Errorf("restic was run as %q", call)
		}
	}
	for _, line := range strings.Split(h.log.String(), "\n") {
		if strings.Contains(line, "step started") || strings.Contains(line, "command_id=c1") && strings.Contains(line, "restic started") {
			h.t.Errorf("the log holds %q", line)
		}
	}
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Scenario: Регистрация по файлу токена заменяет сертификат без роли до проверки роли
func TestEnrollmentByTheTokenFileReplacesACertificateWithoutARoleBeforeTheRoleCheck(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	k := newKey(t)
	writeFile(t, f.h.certFile, certOf(t, k, roleCertNotAfter, testIdentityURI), 0o644)
	writeFile(t, f.h.keyFile, keyPEM(t, k), 0o600)
	tokenFile := writeTokenFile(t, f.h, f.token)
	cfg := writeConfig(t, "server:\n  address: "+f.h.address+"\n"+
		"tls: {ca_file: "+f.h.caFile+", cert_file: "+f.h.certFile+", key_file: "+f.h.keyFile+"}\n"+
		"executor: {state_dir: "+f.h.dir+"/state}\n"+withRestic(t))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := runAgentCmd(ctx, []string{"--config", cfg, "--enroll-token-file", tokenFile}, &out, &errOut, fixedHostname, selfDepsFor(newTickClock()))
	if code != 0 || !strings.Contains(out.String(), " as source\n") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out.String(), errOut.String())
	}
	if f.srv.callCount() != 1 {
		t.Errorf("Enroll calls = %d", f.srv.callCount())
	}
}
