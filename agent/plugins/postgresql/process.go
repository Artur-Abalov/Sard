// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// defaultGrace is how long a stopped process may take to exit after SIGTERM, as for
// restic: psql and pg_dump close their server session meanwhile (F1 ПГ15).
const defaultGrace = 10 * time.Second

// ProcessRunner runs host processes, each in its own process group.
// Cancelling the context sends SIGTERM to the group and SIGKILL to the
// process after Grace. Once the process has exited, whatever is left of its
// group gets SIGKILL.
type ProcessRunner struct {
	Grace time.Duration // zero means 10 seconds
}

// Run implements Runner.
func (r ProcessRunner) Run(ctx context.Context, c Cmd) (int, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = append([]string{}, c.Env...) // non-nil: an empty Env inherits nothing
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &lineWriter{emit: c.Stderr}
	cmd.Stdout, cmd.Stderr = c.Stdout, stderr
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = r.grace()
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	stderr.flush()
	if cmd.ProcessState == nil { // Wait failed before the process was reaped
		return -1, err
	}
	if !cmd.ProcessState.Exited() {
		return -1, errors.New(cmd.ProcessState.String()) // "signal: killed"
	}
	return cmd.ProcessState.ExitCode(), nil
}

func (r ProcessRunner) grace() time.Duration {
	if r.Grace == 0 {
		return defaultGrace
	}
	return r.Grace
}

// signalGroup signals every process of the group led by pid.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	} else if err != nil {
		return err
	}
	return nil
}

// lineWriter splits a stream into lines for a callback.
type lineWriter struct {
	emit func(line string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		line, rest, found := bytes.Cut(w.buf, []byte{'\n'})
		if !found {
			return len(p), nil
		}
		w.send(string(line))
		w.buf = rest
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.send(string(w.buf))
	}
	w.buf = nil
}

func (w *lineWriter) send(line string) {
	if w.emit != nil {
		w.emit(line)
	}
}
