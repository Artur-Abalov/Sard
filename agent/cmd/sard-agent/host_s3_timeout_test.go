// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// virtualClock is the clock of the command in the timing scenarios: time
// passes only when a test advances it.
type virtualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*virtualTimer
}

type virtualTimer struct {
	at time.Time
	ch chan time.Time
}

func newVirtualClock() *virtualClock {
	return &virtualClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
}

func (c *virtualClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &virtualTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t.ch
}

func (c *virtualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// advance lets d pass: the timers that fall due fire.
func (c *virtualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			select {
			case t.ch <- c.now:
			default:
			}
		}
	}
}

// waitTimers waits until n timers have been set.
func (c *virtualClock) waitTimers(t *testing.T, n int) {
	t.Helper()
	for range 2000 {
		c.mu.Lock()
		got := len(c.timers)
		c.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fewer than %d timers were set", n)
}

// runInBackground runs the S3 command line in a goroutine.
func (h *setupHost) runInBackground(args ...string) <-chan cmdResult {
	done := make(chan cmdResult, 1)
	go func() {
		code, stdout, stderr := h.sudo(args...)
		done <- cmdResult{code, stdout, stderr}
	}()
	return done
}

func (h *setupHost) waitForCall(sub string) {
	h.t.Helper()
	for range 2000 {
		if len(h.restic.callsTo(s3Address, sub)) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("restic %s was not called", sub)
}

func (h *setupHost) s3Args(extra ...string) []string {
	h.stdinIs(s3Marker)
	return append([]string{"repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key-stdin", "--config", "C"}, extra...)
}

const retryLine = "Load(<config/0000000000>, 0, 0) returned error, retrying after 1.2s: dial tcp 192.0.2.1:443: connect: connection refused"

// Rule "Проверка доступа ограничена таймаутом подключения, команда не висит на недоступном хранилище".

func TestAnUnreachableS3StorageIsTemporaryAfterTheConnectTimeoutWithTheReasonOfTheRetry(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.s3Repo().hangCat, h.s3Repo().hangLine = true, retryLine
	done := h.runInBackground(h.s3Args("--connect-timeout", "30s")...)
	h.waitForCall("cat")
	vc.waitTimers(t, 2)
	vc.advance(29 * time.Second)
	stillRunning(t, done)
	vc.advance(time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	for _, want := range []string{"did not answer within 30s", "--connect-timeout", "connection refused", s3Address} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	if !h.s3Repo().terminated {
		t.Error("restic did not get SIGTERM")
	}
	if n := len(h.restic.callsTo(s3Address, "init")); n != 0 {
		t.Errorf("restic init was called %d times", n)
	}
	h.assertNoS3Traces()
}

func TestWithoutAFlagTheConnectTimeoutIs30Seconds(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.s3Repo().hangCat = true
	done := h.runInBackground(h.s3Args()...)
	h.waitForCall("cat")
	vc.waitTimers(t, 2)
	vc.advance(29 * time.Second)
	stillRunning(t, done)
	vc.advance(time.Second)
	assertRefusal(t, within(t, done).code, "BACKEND_UNAVAILABLE", exitTemporary, "BACKEND_UNAVAILABLE")
}

func TestTheConnectTimeoutDoesNotLimitTheCreationOfTheRepository(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	gate := make(chan struct{})
	h.s3Repo().initGate = gate
	done := h.runInBackground(h.s3Args("--connect-timeout", "30s")...)
	h.waitForCall("init")
	vc.advance(90 * time.Second)
	stillRunning(t, done)
	close(gate)
	assertCode(t, within(t, done).code, exitOK)
}

func TestAnOverallTimeoutShorterThanTheConnectTimeoutIsTimeout(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.s3Repo().hangCat = true
	done := h.runInBackground(h.s3Args("--connect-timeout", "60s", "--timeout", "20s")...)
	h.waitForCall("cat")
	vc.waitTimers(t, 2)
	vc.advance(20 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "TIMEOUT")
}

// slowTerminal is an operator who takes a while to answer.
type slowTerminal struct {
	*fakeTerminal
	clock *virtualClock
	wait  time.Duration
}

func (s slowTerminal) ReadSecret(prompt string) ([]byte, error) {
	s.clock.advance(s.wait)
	return s.fakeTerminal.ReadSecret(prompt)
}

func TestTheWaitForTheOperatorAtTheTerminalIsNotCountedInTheTimeouts(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	term := slowTerminal{fakeTerminal: &fakeTerminal{answers: []string{s3Marker, s3Marker}}, clock: vc, wait: 5 * time.Minute}
	h.deps.terminal = func(io.Writer) hostsetup.Terminal { return term }
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "TIMEOUT") || strings.Contains(stderr, "BACKEND_UNAVAILABLE") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestTheConnectTimeoutLimitsALocalPathToo(t *testing.T) {
	h := newSetupHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.restic.repo(h.extraDir()).hangCat = true
	done := h.runInBackground("repo", "add", "extra", h.extraDir(), "--connect-timeout", "30s", "--config", "C")
	for len(h.restic.callsTo(h.extraDir(), "cat")) == 0 {
		time.Sleep(time.Millisecond)
	}
	vc.waitTimers(t, 2)
	vc.advance(30 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if !strings.Contains(r.stderr, "--connect-timeout") {
		t.Fatalf("stderr %q", r.stderr)
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestResticIsStoppedWithTheDefaultGraceOfTenSeconds(t *testing.T) {
	// A zero Grace is restic.DefaultGrace, 10 seconds (Р33), see the tests of the executor.
	exec, ok := productionHostDeps().exec.(restic.ProcessExecutor)
	if !ok || exec.Grace != 0 || restic.DefaultGrace != 10*time.Second {
		t.Fatalf("executor %#v", productionHostDeps().exec)
	}
}
