// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The grace of a process stopped by SIGTERM is 10 seconds unless it is set (F1 ПГ15).
func TestGraceOfAProcessIsTenSecondsByDefault(t *testing.T) {
	if got := (ProcessRunner{}).grace(); got != 10*time.Second {
		t.Errorf("default grace = %v", got)
	}
	if got := (ProcessRunner{Grace: time.Second}).grace(); got != time.Second {
		t.Errorf("grace = %v", got)
	}
}

// exitOf tells a process that exited, one killed by a signal, and a wait that failed.
func TestExitOfAProcess(t *testing.T) {
	exited := exec.Command("sh", "-c", "exit 3")
	_ = exited.Run()
	if code, err := exitOf(exited.ProcessState, nil); code != 3 || err != nil {
		t.Errorf("exited: %d, %v", code, err)
	}
	killed := exec.Command("sh", "-c", "kill -9 $$")
	_ = killed.Run()
	if code, err := exitOf(killed.ProcessState, errors.New("exit status -1")); code != -1 || err == nil || err.Error() != "signal: killed" {
		t.Errorf("killed: %d, %v", code, err)
	}
	waitErr := errors.New("wait failed")
	if code, err := exitOf(nil, waitErr); code != -1 || !errors.Is(err, waitErr) {
		t.Errorf("not reaped: %d, %v", code, err)
	}
}

// signalGroup does not mind a group that is gone, and reports any other error.
func TestSignalGroup(t *testing.T) {
	const nobody = 1 << 30 // above any pid
	if err := signalGroup(nobody, syscall.SIGTERM); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("a group that is gone: %v", err)
	}
	// A group that exists, and a signal that does not.
	sleeper := exec.Command("sleep", "60")
	sleeper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	if err := signalGroup(sleeper.Process.Pid, syscall.Signal(99)); err == nil || errors.Is(err, os.ErrProcessDone) {
		t.Errorf("an invalid signal: %v", err)
	}
}
