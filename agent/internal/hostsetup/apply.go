// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Unit is the systemd unit of the agent service.
const Unit = "sard-agent.service"

// restartCommand is what the operator is told to run when the command
// does not restart the service itself.
const restartCommand = "sudo systemctl restart sard-agent"

// Systemd is the host's service manager.
type Systemd interface {
	// Present says whether systemd runs this host (and systemctl exists).
	Present() bool
	// Active says whether the unit is running.
	Active(unit string) (bool, error)
	Restart(unit string) error
	DaemonReload() error
}

// Reload makes systemd read the unit files again (a drop-in changed).
func Reload(sd Systemd) *refusal.Failure {
	if err := sd.DaemonReload(); err != nil {
		return refusal.Fail(refusal.ServiceRestartFailed, "systemctl daemon-reload failed: %v", err)
	}
	return nil
}

// Applier makes a change take effect: it restarts the service, unless
// that would cut a running step off (Р8).
type Applier struct {
	Systemd Systemd
	FS      FS
	// StateDir is executor.state_dir; its journal directory holds the
	// accepted commands that have not finished (ADR 0044).
	StateDir  string
	NoRestart bool
}

// Apply decides in the order of Р8 and tells the operator what happened
// on w. Only a failed restart is a failure; every postponed restart is an
// instruction.
func (a Applier) Apply(w io.Writer) *refusal.Failure {
	switch {
	case a.NoRestart:
		_, _ = fmt.Fprintf(w, "Not restarted (--no-restart): for the change to take effect run `%s`.\n", restartCommand)
	case !a.Systemd.Present():
		_, _ = fmt.Fprintln(w, "systemd is not available on this host: restart the agent yourself for the change to take effect.")
	default:
		return a.applyToService(w)
	}
	return nil
}

func (a Applier) applyToService(w io.Writer) *refusal.Failure {
	active, err := a.Systemd.Active(Unit)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "Cannot tell whether %s runs (%v): not restarted. Run `%s` for the change to take effect.\n", Unit, err, restartCommand)
		return nil
	case !active:
		_, _ = fmt.Fprintf(w, "The service %s is not running: the change takes effect when the service starts.\n", Unit)
		return nil
	}
	return a.restartIfIdle(w)
}

func (a Applier) restartIfIdle(w io.Writer) *refusal.Failure {
	journal := filepath.Join(a.StateDir, "journal")
	steps, err := countSteps(a.FS, journal)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(w, "The command journal %s cannot be read (%v): the service was not restarted. When no step is running, run `%s` for the change to take effect.\n", journal, err, restartCommand)
	case steps > 0:
		_, _ = fmt.Fprintf(w, "%s: the service was not restarted so as not to cut them off. When they have finished, run `%s` for the change to take effect.\n", stepsRunning(steps), restartCommand)
	default:
		return a.restart(w)
	}
	return nil
}

func (a Applier) restart(w io.Writer) *refusal.Failure {
	if err := a.Systemd.Restart(Unit); err != nil {
		return refusal.Fail(refusal.ServiceRestartFailed, "systemctl restart %s failed: %v; the change is saved, see `journalctl -u sard-agent`", Unit, err)
	}
	_, _ = fmt.Fprintf(w, "Service %s restarted.\n", Unit)
	return nil
}

func stepsRunning(n int) string {
	if n == 1 {
		return "1 step is running"
	}
	return fmt.Sprintf("%d steps are running", n)
}

// countSteps is the number of accepted commands in the journal: files
// *.json that are not temporary (their name does not start with a dot).
// A missing directory holds none.
func countSteps(fsys FS, journal string) (int, error) {
	entries, err := fsys.ReadDir(journal)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if isJournalEntry(e) {
			n++
		}
	}
	return n, nil
}

// isJournalEntry: a file *.json that is not a temporary one.
func isJournalEntry(e fs.DirEntry) bool {
	return !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".")
}
