// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

type runLog struct {
	calls []string
	err   error
}

func (r *runLog) run(name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return r.err
}

func lookPath(found bool) func(string) (string, error) {
	return func(name string) (string, error) {
		if !found {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
}

func TestSystemdIsPresentWithItsRunDirectoryAndSystemctl(t *testing.T) {
	runDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "system")
	for name, c := range map[string]struct {
		dir   string
		found bool
		want  bool
	}{
		"both":           {runDir, true, true},
		"no directory":   {missing, true, false},
		"no systemctl":   {runDir, false, false},
		"neither of the": {missing, false, false},
	} {
		sd := hostsetup.ExecSystemd{RunDir: c.dir, LookPath: lookPath(c.found)}
		if got := sd.Present(); got != c.want {
			t.Errorf("%s: Present() = %v, want %v", name, got, c.want)
		}
	}
}

func TestSystemctlCommands(t *testing.T) {
	log := &runLog{}
	sd := hostsetup.ExecSystemd{Run: log.run}
	if err := sd.Restart("sard-agent.service"); err != nil {
		t.Fatal(err)
	}
	if err := sd.DaemonReload(); err != nil {
		t.Fatal(err)
	}
	if active, err := sd.Active("sard-agent.service"); err != nil || !active {
		t.Fatalf("active %v, err %v", active, err)
	}
	want := []string{"systemctl restart sard-agent.service", "systemctl daemon-reload", "systemctl is-active --quiet sard-agent.service"}
	if strings.Join(log.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %q", log.calls)
	}
}

func TestSystemctlFailuresAreReported(t *testing.T) {
	boom := errors.New("boom")
	sd := hostsetup.ExecSystemd{Run: (&runLog{err: boom}).run}
	if err := sd.Restart("u"); !errors.Is(err, boom) {
		t.Fatalf("restart: %v", err)
	}
	if err := sd.DaemonReload(); !errors.Is(err, boom) {
		t.Fatalf("reload: %v", err)
	}
	if _, err := sd.Active("u"); !errors.Is(err, boom) {
		t.Fatalf("is-active: %v", err)
	}
}

func TestInactiveUnitIsNotAnError(t *testing.T) {
	// A real process that exits with a non-zero status: systemctl is-active
	// does exactly this for a unit that is not running.
	err := hostsetup.RunCommand("sh", "-c", "exit 3")
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("RunCommand returned %v", err)
	}
	sd := hostsetup.ExecSystemd{Run: func(string, ...string) error { return err }}
	if active, aerr := sd.Active("u"); aerr != nil || active {
		t.Fatalf("active %v, err %v", active, aerr)
	}
	if err := hostsetup.RunCommand("sh", "-c", "exit 0"); err != nil {
		t.Fatal(err)
	}
}
