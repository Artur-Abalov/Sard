// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// fakeEnrollClock fires its timer only when a test asks it to, so --timeout
// scenarios do not depend on waiting in real time.
type fakeEnrollClock struct {
	added chan time.Duration
	fire  chan time.Time
}

func newFakeEnrollClock() *fakeEnrollClock {
	return &fakeEnrollClock{added: make(chan time.Duration, 4), fire: make(chan time.Time, 4)}
}

func (c *fakeEnrollClock) After(d time.Duration) <-chan time.Time {
	c.added <- d
	return c.fire
}

func (c *fakeEnrollClock) waitTimer(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.added:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("no timer was set")
		return 0
	}
}

func (c *fakeEnrollClock) fireNow() { c.fire <- time.Now() }

func runEnrollCmdWithClock(clk clock, args ...string) (int, string, string) {
	var out, errOut strings.Builder
	code := runEnrollWithClock(context.Background(), args, &out, &errOut, fixedHostname, clk)
	return code, out.String(), errOut.String()
}

func newBlockingFixture(t *testing.T) (*host, string, *enrollServer) {
	t.Helper()
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
	srv := &enrollServer{started: make(chan struct{}), block: make(chan struct{})}
	srv.answer = succeedingAnswer(ca, "a1")
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	return h, token, srv
}

// Без флага таймаута команда ждёт сервер 30 секунд
func TestWithoutATimeoutFlagTheCommandWaits30Seconds(t *testing.T) {
	h, token, _ := newBlockingFixture(t)
	clk := newFakeEnrollClock()
	done := make(chan int, 1)
	go func() {
		code, _, _ := runEnrollCmdWithClock(clk, "--config", h.configPath, "--token", token)
		done <- code
	}()
	d := clk.waitTimer(t)
	if d != defaultEnrollTimeout {
		t.Fatalf("timer duration = %v, want %v", d, defaultEnrollTimeout)
	}
	select {
	case <-done:
		t.Fatal("the command finished before the clock advanced")
	case <-time.After(50 * time.Millisecond):
	}
	clk.fireNow()
	select {
	case code := <-done:
		if code != exitTemporary {
			t.Fatalf("code = %d, want %d (temporary)", code, exitTemporary)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command did not finish after the clock advanced")
	}
}

// Флаг таймаута меняет время ожидания
func TestTheTimeoutFlagChangesHowLongTheCommandWaits(t *testing.T) {
	h, token, _ := newBlockingFixture(t)
	clk := newFakeEnrollClock()
	done := make(chan int, 1)
	go func() {
		code, _, _ := runEnrollCmdWithClock(clk, "--config", h.configPath, "--token", token, "--timeout", "5s")
		done <- code
	}()
	if d := clk.waitTimer(t); d != 5*time.Second {
		t.Fatalf("timer duration = %v, want 5s", d)
	}
	clk.fireNow()
	select {
	case code := <-done:
		if code != exitTemporary {
			t.Fatalf("code = %d, want %d (temporary)", code, exitTemporary)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command did not finish after the clock advanced")
	}
}

// Таймаут после отправки регистрации предупреждает, что токен мог быть израсходован
func TestATimeoutAfterSendingEnrollWarnsTheTokenMayHaveBeenSpent(t *testing.T) {
	h, token, srv := newBlockingFixture(t)
	clk := newFakeEnrollClock()
	var out, errOut strings.Builder
	done := make(chan int, 1)
	go func() {
		code := runEnrollWithClock(context.Background(), []string{"--config", h.configPath, "--token", token}, &out, &errOut, fixedHostname, clk)
		done <- code
	}()
	<-srv.started // the server has received the call and is holding it
	clk.waitTimer(t)
	clk.fireNow()
	code := <-done
	if code != exitTemporary {
		t.Fatalf("code = %d, want %d (temporary); stderr = %q", code, exitTemporary, errOut.String())
	}
	if !strings.Contains(errOut.String(), "may have been spent") || !strings.Contains(errOut.String(), "TOKEN_USED") {
		t.Errorf("stderr does not warn the token may have been spent: %q", errOut.String())
	}
}

// Прерывание команды сигналом не меняет хост
func TestInterruptingTheCommandDoesNotChangeTheHost(t *testing.T) {
	h, token, srv := newBlockingFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut strings.Builder
	done := make(chan int, 1)
	go func() {
		code := runEnroll(ctx, []string{"--config", h.configPath, "--token", token}, &out, &errOut, fixedHostname)
		done <- code
	}()
	<-srv.started
	cancel() // simulates SIGINT: the outer context main() sets up ends the same way
	code := <-done
	if code != exitTemporary {
		t.Fatalf("code = %d, want %d (temporary); stderr = %q", code, exitTemporary, errOut.String())
	}
	if !strings.Contains(errOut.String(), "may have been spent") {
		t.Errorf("stderr does not warn the token may have been spent: %q", errOut.String())
	}
	for _, p := range []string{h.keyFile, h.certFile, h.caFile} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s exists after an interrupted command, want the host untouched", p)
		}
	}
}

// Вторая одновременная регистрация отказывает до обращения к серверу
func TestASecondConcurrentEnrollmentIsRefusedBeforeContactingTheServer(t *testing.T) {
	h, token1, srv := newBlockingFixture(t)
	done := make(chan int, 1)
	go func() {
		code, _, _ := runEnrollCmdTest("--config", h.configPath, "--token", token1)
		done <- code
	}()
	<-srv.started // the first command holds the lock and is mid-request

	token2 := newToken(t, strings.Repeat("a", 64)) // any second active token
	code2, _, errOut2 := runEnrollCmdTest("--config", h.configPath, "--token", token2)
	if code2 != exitTemporary {
		t.Fatalf("second command: code = %d, want %d (temporary); stderr = %q", code2, exitTemporary, errOut2)
	}
	if !strings.Contains(errOut2, "already") {
		t.Errorf("second command's stderr does not say an enrollment is already running: %q", errOut2)
	}

	close(srv.block)
	code1 := <-done
	if code1 != exitOK {
		t.Fatalf("first command: code = %d, want 0", code1)
	}
	if srv.callCount() != 1 {
		t.Fatalf("server calls = %d, want exactly 1 (only the first command)", srv.callCount())
	}
}

// Одновременные регистрации на одном хосте не смешивают файлы
func TestConcurrentEnrollmentsOnOneHostDoNotMixFiles(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	token2 := newToken(t, f.ca.fingerprint())
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0], _, _ = runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	}()
	go func() {
		defer wg.Done()
		codes[1], _, _ = runEnrollCmdTest("--config", f.h.configPath, "--token", token2)
	}()
	wg.Wait()

	successes := 0
	for _, c := range codes {
		if c == exitOK {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1 (codes: %v)", successes, codes)
	}
	if _, err := os.Stat(enroll.LockPath(f.h.certFile)); !os.IsNotExist(err) {
		t.Fatal("a lock file was left behind")
	}
}
