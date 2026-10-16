// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// docs/specs/agent/agent-certificate-renewal.feature, tag @start.

// runOn starts the agent on the host's files through the file system fsys.
func (h *renewalHost) runOn(fsys certpair.FS) *runningAgent {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a := &runningAgent{cancel: cancel, done: make(chan int, 1)}
	d := h.deps()
	d.runtime.fs = fsys
	go func() {
		a.done <- runAgentCmd(ctx, []string{"--config", h.cfg}, &a.stdout, &a.stderr, fixedHostname, d)
	}()
	h.t.Cleanup(func() { cancel(); a.wait(h.t) })
	return a
}

// renameFails is the host file system that cannot move a file into place.
type renameFails struct{ certpair.OSFS }

func (renameFails) Rename(string, string) error {
	return &fs.PathError{Op: "rename", Path: "tls", Err: fs.ErrPermission}
}

func refusedForGood() func(string, int) error {
	return func(string, int) error {
		return refusedWith(codes.Unauthenticated, "no", "sard.dev", "CERT_REVOKED", nil)(nil)
	}
}

// Scenario: Окончательный отказ в подтверждении на старте возвращает агента к текущей паре
func TestAFinalRefusalAtConfirmationStopsTheAgentWhoseCurrentCertificateHasExpired(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(250), renewDay(550)), ".new")
	h.clock.Set(renewDay(301))
	h.server.confirm = refusedForGood()
	a := h.run()
	h.requireRefusalLine(a, "CERT_EXPIRED", h.path("agent.pem"))
	if h.server.count("register") != 0 || h.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("events = %v, files = %s", h.server.eventLog(), h.names())
	}
}

// Scenario: Окончательный отказ в подтверждении на старте возвращает агента к текущей паре
func TestAFinalRefusalAtConfirmationStopsTheAgentWhoseCurrentPairIsNotAPair(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(100), renewDay(400)), ".new")
	h.writeFile("agent.key", h.issuePair(3, renewT0, renewDay(300)).KeyPEM, 0o600)
	h.clock.Set(renewDay(101))
	h.server.confirm = refusedForGood()
	a := h.run()
	if code := a.wait(t); code != 1 || !strings.Contains(a.stderr.String(), incompleteEnrollment) || h.server.count("register") != 0 {
		t.Fatalf("code = %d, stderr = %q, events = %v", code, a.stderr.String(), h.server.eventLog())
	}
}

// Scenario: Сервер недоступен при подтверждении на старте, и агент не регистрируется старой парой
func TestAnAgentStoppedWhileItWaitsForTheConfirmationEndsWithCode0(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(200), renewDay(500)), ".new")
	h.clock.Set(renewDay(201))
	h.server.confirm = func(string, int) error { return status.Error(codes.Unavailable, "down") }
	a := h.run()
	h.clock.WaitTimer(t) // the pause before the next confirmation
	if code := a.stop(t); code != 0 || a.stderr.String() != "" {
		t.Fatalf("code = %d, stderr = %q", code, a.stderr.String())
	}
	if h.server.count("register") != 0 {
		t.Fatalf("events = %v", h.server.eventLog())
	}
}

// Scenario: Перезапуск между двумя переименованиями
func TestARenewedPairThatCannotBeMovedIntoPlaceEndsTheAgentBeforeRegistering(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(200), renewDay(500)), ".new")
	h.clock.Set(renewDay(201))
	a := h.runOn(renameFails{})
	if code := a.wait(t); code != 1 || !strings.Contains(a.stderr.String(), "rename") || h.server.count("register") != 0 {
		t.Fatalf("code = %d, stderr = %q, events = %v", code, a.stderr.String(), h.server.eventLog())
	}
}

// Scenario: Агент проверяет права файлов выбранной пары
func TestTheModesAreCheckedOnTheFilesOfTheSelectedPair(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(200), renewDay(500)), ".new")
	h.clock.Set(renewDay(201))
	if err := os.Chmod(h.path("agent.key.new"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := h.run()
	if code := a.wait(t); code != 1 || !strings.Contains(a.stderr.String(), h.path("agent.key.new")) || len(h.server.eventLog()) != 0 {
		t.Fatalf("code = %d, stderr = %q, events = %v", code, a.stderr.String(), h.server.eventLog())
	}
}

// Scenario: Агент проверяет права файлов выбранной пары
func TestTheModeOfTheKeyThatIsNotSelectedDoesNotStopTheAgent(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(200), renewDay(500)), ".new")
	h.clock.Set(renewDay(201))
	if err := os.Chmod(h.path("agent.key"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C2")
}

// Scenario: Недописанная новая пара удаляется, агент работает с текущей
func TestWithoutStaleFilesTheAgentLogsNoRemoval(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.run()
	h.server.waitCount(t, "register", 1)
	if log := h.log.String(); strings.Contains(log, "removed renewal files") {
		t.Fatalf("log = %s", log)
	}
}

func TestTheDefaultsOfTheRuntimeAreTheProductionOnes(t *testing.T) {
	t.Parallel()
	got := agentRuntime{}.withDefaults()
	if _, ok := got.clock.(realEnrollClock); !ok || got.rand == nil || got.random != rand.Reader || got.logger != slog.Default() {
		t.Fatalf("defaults = %+v", got)
	}
	if _, ok := got.fs.(certpair.OSFS); !ok {
		t.Fatalf("fs = %T", got.fs)
	}
}

func TestTheRuntimeKeepsWhatItIsGiven(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	fsys := renameFails{}
	given := agentRuntime{rand: func() float64 { return 0.25 }, random: strings.NewReader("x"), logger: logger, fs: fsys}
	got := given.withDefaults()
	if got.rand() != 0.25 || got.random != given.random || got.logger != logger || got.fs != fsys {
		t.Fatalf("runtime = %+v", got)
	}
}

// slowCaller is a certificate renewal whose call ends late, after the agent was told to stop.
type slowCaller struct {
	finished *atomic.Bool
}

func (c slowCaller) Renew(ctx context.Context, _, _ string, _ []byte) (string, error) {
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	c.finished.Store(true)
	return "", ctx.Err()
}

func (slowCaller) Confirm(context.Context, string, string) error { return nil }

func TestTheAgentEndsOnlyWhenTheRenewalBesideItHasEnded(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	cfg, err := config.Load(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var finished atomic.Bool
	renewal := certrenew.New(certrenew.Options{
		Files: cfg.TLS, StatePath: h.path("state.json"), FS: certpair.OSFS{}, Caller: slowCaller{&finished}, Clock: h.clock,
	})
	renewal.Registered()
	h.clock.Set(renewDay(200)) // the threshold of pair 1: the renewal is due at once
	ran := make(chan struct{})
	err = runRenewing(context.Background(), renewal, func(context.Context) error {
		time.Sleep(200 * time.Millisecond) // the renewal is in its call by now
		close(ran)
		return errors.New("the agent stopped")
	})
	if err == nil || err.Error() != "the agent stopped" || !finished.Load() {
		t.Fatalf("err = %v, the renewal finished = %v", err, finished.Load())
	}
	<-ran
}

// pollNewToken lets the built-in agent look at its token file n times.
func (h *renewalHost) pollNewToken(n int) {
	h.t.Helper()
	for range n {
		h.clock.Advance(selfEnrollPollInterval)
		time.Sleep(10 * time.Millisecond)
	}
}

// Scenario: Новый токен в файле выводит встроенный агент из отказа
func TestTheRefusedBuiltInAgentWaitsWhileThereIsNoNewToken(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("AGENT_REVOKED")
	tokenFile := h.builtIn("spent-token")
	a := h.run("--enroll-token-file", tokenFile)
	a.waitStderr(t, "AGENT_REVOKED", 1)
	steps := []struct {
		name  string
		setup func() error
	}{
		{"the spent token", func() error { return nil }},
		{"an empty file", func() error { return writeTokenText(tokenFile, "") }},
		{"no file", func() error { return os.Remove(tokenFile) }},
	}
	for _, step := range steps {
		if err := step.setup(); err != nil {
			t.Fatal(err)
		}
		h.pollNewToken(5)
		if a.exited() || h.server.count("enroll") != 0 || h.server.count("register") != 1 {
			t.Fatalf("%s: exited = %v, stderr = %q, events = %v", step.name, a.exited(), a.stderr.String(), h.server.eventLog())
		}
	}
}

func TestAConfigThatCannotBeLoadedEndsTheRefusedBuiltInAgentWithAnError(t *testing.T) {
	t.Parallel()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr strings.Builder
	refused := &refusedError{detail: "the server said no", reenroll: "run enroll"}
	proceed, code := rejectedUntilNewToken(cancelled, t.TempDir()+"/absent.yaml", t.TempDir()+"/token", refused, &stdout, &stderr, selfDepsFor(realEnrollClock{}))
	if proceed || code != exitError || !strings.Contains(stderr.String(), "absent.yaml") || strings.Contains(stderr.String(), "the server said no") {
		t.Fatalf("proceed = %v, code = %d, stderr = %q", proceed, code, stderr.String())
	}
}
