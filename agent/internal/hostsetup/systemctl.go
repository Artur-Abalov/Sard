// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"os"
	"os/exec"
)

// ExecSystemd is Systemd through the systemctl command.
type ExecSystemd struct {
	// RunDir is the directory systemd creates when it is the init system
	// (/run/systemd/system).
	RunDir   string
	LookPath func(name string) (string, error)
	// Run runs a command to completion: nil for exit status 0, an
	// *exec.ExitError for another status.
	Run func(name string, args ...string) error
}

// NewExecSystemd is ExecSystemd on this host.
func NewExecSystemd() ExecSystemd {
	return ExecSystemd{RunDir: "/run/systemd/system", LookPath: exec.LookPath, Run: RunCommand}
}

// RunCommand runs the command with no input and no output kept.
func RunCommand(name string, args ...string) error { return exec.Command(name, args...).Run() }

// Present implements Systemd.
func (s ExecSystemd) Present() bool {
	if _, err := os.Stat(s.RunDir); err != nil {
		return false
	}
	_, err := s.LookPath("systemctl")
	return err == nil
}

// Active implements Systemd: "systemctl is-active" exits non-zero for a
// unit that is not running, which is an answer, not a failure.
func (s ExecSystemd) Active(unit string) (bool, error) {
	err := s.Run("systemctl", "is-active", "--quiet", unit)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return err == nil, err
}

// Restart implements Systemd.
func (s ExecSystemd) Restart(unit string) error { return s.Run("systemctl", "restart", unit) }

// DaemonReload implements Systemd.
func (s ExecSystemd) DaemonReload() error { return s.Run("systemctl", "daemon-reload") }
