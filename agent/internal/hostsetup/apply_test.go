// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// fakeSystemd records what it was asked.
type fakeSystemd struct {
	present    bool
	active     bool
	activeErr  error
	restartErr error
	reloadErr  error
	calls      []string
}

func (f *fakeSystemd) Present() bool { return f.present }

func (f *fakeSystemd) Active(unit string) (bool, error) {
	f.calls = append(f.calls, "is-active "+unit)
	return f.active, f.activeErr
}

func (f *fakeSystemd) Restart(unit string) error {
	f.calls = append(f.calls, "restart "+unit)
	return f.restartErr
}

func (f *fakeSystemd) DaemonReload() error {
	f.calls = append(f.calls, "daemon-reload")
	return f.reloadErr
}

func (f *fakeSystemd) restarted() bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "restart") {
			return true
		}
	}
	return false
}

type readDirFailsFS struct {
	hostsetup.OS
	err error
}

func (r readDirFailsFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, r.err }

// stateDir is S with the given journal files.
func stateDir(t *testing.T, journal ...string) string {
	t.Helper()
	s := t.TempDir()
	if err := os.MkdirAll(filepath.Join(s, "journal"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range journal {
		if err := os.WriteFile(filepath.Join(s, "journal", name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func apply(t *testing.T, sd *fakeSystemd, fsys hostsetup.FS, s string, noRestart bool) (string, *repoinit.Failure) {
	t.Helper()
	var out bytes.Buffer
	f := hostsetup.Applier{Systemd: sd, FS: fsys, StateDir: s, NoRestart: noRestart}.Apply(&out)
	return out.String(), f
}

func TestWithoutRunningStepsTheServiceIsRestarted(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t), false)
	if f != nil || !sd.restarted() || !strings.Contains(out, "restarted") || sd.calls[len(sd.calls)-1] != "restart sard-agent.service" {
		t.Fatalf("calls %v, out %q, refusal %v", sd.calls, out, f)
	}
}

func TestARunningStepPostponesTheRestartAndTheCommandSaysHow(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t, "a.json", "b.json"), false)
	if f != nil || sd.restarted() {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
	for _, want := range []string{"2 steps are running", "not restarted", "sudo systemctl restart sard-agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q lacks %q", out, want)
		}
	}
}

func TestOneRunningStepIsInTheSingular(t *testing.T) {
	out, _ := apply(t, &fakeSystemd{present: true, active: true}, hostsetup.OS{}, stateDir(t, "a.json"), false)
	if !strings.Contains(out, "1 step is running") {
		t.Fatalf("out %q", out)
	}
}

func TestTemporaryFilesOfTheJournalAreNotSteps(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	if _, f := apply(t, sd, hostsetup.OS{}, stateDir(t, ".tmp-123", ".a.json", "notes.txt"), false); f != nil || !sd.restarted() {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
}

func TestNoRestartFlagNeverRestarts(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t), true)
	if f != nil || len(sd.calls) != 0 || !strings.Contains(out, "sudo systemctl restart sard-agent") {
		t.Fatalf("calls %v, out %q, refusal %v", sd.calls, out, f)
	}
}

func TestAStoppedServiceIsNotStarted(t *testing.T) {
	sd := &fakeSystemd{present: true, active: false}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t), false)
	if f != nil || sd.restarted() || !strings.Contains(out, "takes effect when the service starts") {
		t.Fatalf("calls %v, out %q, refusal %v", sd.calls, out, f)
	}
}

func TestWithoutSystemdTheOperatorRestartsTheAgent(t *testing.T) {
	sd := &fakeSystemd{present: false}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t), false)
	if f != nil || len(sd.calls) != 0 || !strings.Contains(out, "restart the agent yourself") {
		t.Fatalf("calls %v, out %q, refusal %v", sd.calls, out, f)
	}
}

func TestAnUnreadableJournalPostponesTheRestartAndNamesIt(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	s := stateDir(t)
	out, f := apply(t, sd, readDirFailsFS{err: errors.New("permission denied")}, s, false)
	journal := filepath.Join(s, "journal")
	for _, want := range []string{journal, "permission denied", "sudo systemctl restart sard-agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("out %q lacks %q", out, want)
		}
	}
	if f != nil || sd.restarted() {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
}

func TestAMissingStateDirectoryMeansNoSteps(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true}
	if _, f := apply(t, sd, hostsetup.OS{}, filepath.Join(t.TempDir(), "absent"), false); f != nil || !sd.restarted() {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
}

func TestAFailedRestartIsAnAgentErrorThatPointsAtTheJournal(t *testing.T) {
	sd := &fakeSystemd{present: true, active: true, restartErr: errors.New("exit status 1")}
	_, f := apply(t, sd, hostsetup.OS{}, stateDir(t), false)
	if f == nil || f.Reason != repoinit.ServiceRestartFailed || f.Class != repoinit.ClassAgentError || !strings.Contains(f.Detail, "journalctl -u sard-agent") {
		t.Fatalf("refusal %+v", f)
	}
}

func TestWhenTheStateOfTheServiceIsUnknownTheOperatorRestarts(t *testing.T) {
	sd := &fakeSystemd{present: true, activeErr: errors.New("systemctl: not found")}
	out, f := apply(t, sd, hostsetup.OS{}, stateDir(t), false)
	if f != nil || sd.restarted() || !strings.Contains(out, "systemctl: not found") || !strings.Contains(out, "sudo systemctl restart sard-agent") {
		t.Fatalf("calls %v, out %q, refusal %v", sd.calls, out, f)
	}
}

func TestADirectoryNamedLikeAJournalEntryIsNotAStep(t *testing.T) {
	s := stateDir(t)
	if err := os.Mkdir(filepath.Join(s, "journal", "x.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	sd := &fakeSystemd{present: true, active: true}
	if _, f := apply(t, sd, hostsetup.OS{}, s, false); f != nil || !sd.restarted() {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
}

func TestReloadIsOneCallAndItsFailureIsAWriteProblemOfTheService(t *testing.T) {
	sd := &fakeSystemd{present: true}
	if f := hostsetup.Reload(sd); f != nil || sd.calls[0] != "daemon-reload" {
		t.Fatalf("calls %v, refusal %v", sd.calls, f)
	}
	sd = &fakeSystemd{present: true, reloadErr: errors.New("busy")}
	if f := hostsetup.Reload(sd); f == nil || f.Reason != repoinit.ServiceRestartFailed || !strings.Contains(f.Detail, "daemon-reload") {
		t.Fatalf("refusal %+v", f)
	}
}
