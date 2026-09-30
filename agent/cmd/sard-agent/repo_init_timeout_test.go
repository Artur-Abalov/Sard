// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

type cmdResult struct {
	code           int
	stdout, stderr string
}

// start runs the command in the background.
func (h *repoHost) start(ctx context.Context, args ...string) <-chan cmdResult {
	done := make(chan cmdResult, 1)
	go func() {
		code, stdout, stderr := h.runCtx(ctx, args...)
		done <- cmdResult{code, stdout, stderr}
	}()
	return done
}

func within(t *testing.T, done <-chan cmdResult) cmdResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not finish")
		return cmdResult{}
	}
}

func stillRunning(t *testing.T, done <-chan cmdResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("the command finished early: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

// Rule "Таймаут ограничивает всю команду".

// Без флага таймаута команда ждёт бэкенд 2 минуты
func TestWithoutATimeoutFlagTheCommandWaitsForTheBackendFor2Minutes(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	done := h.start(context.Background(), "init", "--config", "C", "main")
	if d := h.clock.waitTimer(t); d != 2*time.Minute {
		t.Fatalf("timer = %v, want 2m", d)
	}
	<-h.main().initEntered
	stillRunning(t, done)
	h.clock.fireNow()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	assertReason(t, r.stderr, "TIMEOUT")
	if !strings.Contains(r.stderr, "backend local") {
		t.Errorf("the message does not name the backend type: %q", r.stderr)
	}
}

// Флаг таймаута меняет время ожидания
func TestTheTimeoutFlagChangesTheWait(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	done := h.start(context.Background(), "init", "--config", "C", "--timeout", "5s", "main")
	if d := h.clock.waitTimer(t); d != 5*time.Second {
		t.Fatalf("timer = %v, want 5s", d)
	}
	<-h.main().initEntered
	h.clock.fireNow()
	assertCode(t, within(t, done).code, exitTemporary)
}

// Истечение таймаута останавливает restic и не выдаёт успех
func TestATimeoutStopsResticAndIsNoSuccess(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	before := h.snapshot()
	done := h.start(context.Background(), "init", "--config", "C", "main")
	<-h.main().initEntered
	h.clock.fireNow()
	r := within(t, done)
	if !h.main().terminated {
		t.Error("restic did not get SIGTERM")
	}
	if !strings.Contains(r.stderr, "may have been created partially") || !strings.Contains(r.stderr, "run the command again") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if strings.Contains(r.stdout, "repository_id") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.assertUnchanged(before)
}

// Таймаут во время проверки существующего репозитория — временная ошибка
func TestATimeoutWhileCheckingTheExistingRepositoryIsTemporary(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangCat = true
	done := h.start(context.Background(), "init", "--config", "C", "main")
	h.clock.waitTimer(t)
	for len(h.restic.callsTo(h.repoURL(), "cat")) == 0 {
		time.Sleep(time.Millisecond)
	}
	h.clock.fireNow()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	if n := len(h.restic.callsTo(h.repoURL(), "init")); n != 0 {
		t.Fatalf("restic init was called %d times", n)
	}
}

// Прерывание команды останавливает restic и не выдаёт успех
func TestAnInterruptStopsResticAndIsNoSuccess(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	before := h.snapshot()
	ctx, interrupt := context.WithCancel(context.Background())
	done := h.start(ctx, "init", "--config", "C", "main")
	<-h.main().initEntered
	interrupt()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	assertReason(t, r.stderr, "INTERRUPTED")
	if !h.main().terminated || !strings.Contains(r.stderr, "may have been created partially") {
		t.Errorf("terminated = %v, stderr = %q", h.main().terminated, r.stderr)
	}
	if strings.Contains(r.stdout, "repository_id") {
		t.Errorf("stdout = %q", r.stdout)
	}
	h.assertUnchanged(before)
}

// Rule "Один репозиторий на хосте инициализирует одна команда".

// Вторая одновременная инициализация того же репозитория отказывает до обращения к бэкенду
func TestASecondConcurrentInitOfTheSameRepositoryIsRefusedBeforeTheBackend(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	ctx, stop := context.WithCancel(context.Background())
	first := h.start(ctx, "init", "--config", "C", "main")
	<-h.main().initEntered
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitTemporary)
	assertReason(t, stderr, "INIT_IN_PROGRESS")
	if n := len(h.restic.callsTo(h.repoURL(), "init")); n != 1 {
		t.Errorf("restic init was called %d times, want only the first command's", n)
	}
	stop()
	within(t, first)
}

// Инициализации разных репозиториев одного хоста не мешают друг другу
func TestInitsOfDifferentRepositoriesDoNotBlockEachOther(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	ctx, stop := context.WithCancel(context.Background())
	first := h.start(ctx, "init", "--config", "C", "main")
	<-h.main().initEntered
	code, _, stderr := h.run("init", "--config", "C", "offsite")
	if strings.Contains(stderr, "INIT_IN_PROGRESS") || code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	stop()
	within(t, first)
}

// После завершения первой команды блокировка не мешает повтору
func TestTheLockDoesNotHinderARepeatAfterTheFirstCommandEnded(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangInit = true
	first := h.start(context.Background(), "init", "--config", "C", "main")
	<-h.main().initEntered
	h.clock.fireNow()
	within(t, first)
	h.main().hangInit = false
	code, _, stderr := h.initCmd()
	if strings.Contains(stderr, "INIT_IN_PROGRESS") || code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	entries, _ := os.ReadDir(h.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") {
			t.Errorf("lock file %s is left", e.Name())
		}
	}
}
