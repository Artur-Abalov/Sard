// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Executor runs a process to completion. The wrapper depends on this
// interface only, so parsing is tested without starting restic.
type Executor interface {
	// Run starts cmd, feeds its output line by line to the callbacks and
	// returns the exit code: -1 when the process was killed by a signal or
	// did not start (err says why).
	Run(ctx context.Context, cmd Command) (exitCode int, err error)
}

// Command is one process invocation.
type Command struct {
	Path string
	Args []string
	// Env is the complete environment; nothing is inherited from the agent.
	Env []string
	// Stdin is the process's standard input; nil is an empty one. An
	// *os.File is handed to the process as is.
	Stdin io.Reader
	// Stdout and Stderr receive each output line without its line ending.
	// They are called from separate goroutines; a line is valid only
	// during the call. Nil discards the stream.
	Stdout func(line []byte)
	Stderr func(line []byte)
}

// DefaultGrace is how long a cancelled process may take to exit after
// SIGTERM; restic uses it to remove its lock.
const DefaultGrace = 10 * time.Second

// ProcessExecutor runs processes in their own process group. Cancelling
// the context sends SIGTERM to the group and SIGKILL to the process after
// Grace. Once the process has exited, whatever is left of its group gets
// SIGKILL, and output still held open by it is abandoned after Grace.
type ProcessExecutor struct {
	Grace time.Duration // zero means DefaultGrace
}

// Run implements Executor.
func (p ProcessExecutor) Run(ctx context.Context, c Command) (int, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = append([]string{}, c.Env...) // non-nil: an empty Env inherits nothing
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, stderr := &lineWriter{emit: c.Stdout}, &lineWriter{emit: c.Stderr}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, stdout, stderr
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = p.grace()
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	err := cmd.Wait()
	// Whatever restic left behind in its group is an orphan now.
	_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	stdout.flush()
	stderr.flush()
	if cmd.ProcessState == nil { // Wait failed before the process was reaped
		return -1, err
	}
	return cmd.ProcessState.ExitCode(), nil
}

func (p ProcessExecutor) grace() time.Duration {
	if p.Grace == 0 {
		return DefaultGrace
	}
	return p.Grace
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
	emit func(line []byte)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		line, rest, found := bytes.Cut(w.buf, []byte{'\n'})
		if !found {
			return len(p), nil
		}
		w.send(line)
		w.buf = rest
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.send(w.buf)
	}
	w.buf = nil
}

// send drops the carriage returns restic writes around progress lines.
func (w *lineWriter) send(line []byte) {
	if w.emit != nil {
		w.emit(bytes.Trim(line, "\r"))
	}
}
