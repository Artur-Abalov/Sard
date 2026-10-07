// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// useTestServiceUser makes the user of the test process the service user:
// the repo-init.feature convention (A8a, Р24) — commands run as the
// service user, or as root when the test is run as root.
func useTestServiceUser(d *hostDeps) {
	d.euid = uint32(os.Getuid())
	self := hostsetup.User{Name: "sard-agent", UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	d.lookupUser = users(self)
	d.systemd = &fakeSystemd{}
	d.openAudit = (&fakeSyslog{}).open
	d.getenv = func(string) string { return "" }
	d.processUser = func() (string, uint32) { return "tester", uint32(os.Getuid()) }
}

// The users of the conventions of host-setup.feature: the service user U,
// the operator alice, a user backup for service.user.
const (
	serviceUID = 990
	aliceUID   = 1000
	backupUID  = 991
)

var (
	svcUser    = hostsetup.User{Name: "sard-agent", UID: serviceUID, GID: serviceUID}
	backupUser = hostsetup.User{Name: "backup", UID: backupUID, GID: backupUID}
)

// setupHost is the world of the host-setup scenarios: the conventions
// section of docs/specs/agent/host-setup.feature in a temporary directory D.
type setupHost struct {
	*repoHost
	fsys   *fakeFS
	sd     *fakeSystemd
	log    *fakeSyslog
	term   *fakeTerminal
	stdin  *bytes.Buffer
	hung   *hungInput
	env    map[string]string
	people []hostsetup.User
	// readOwned and plainReads are the paths read through the owner-checked
	// read (with the uid asked for) and through the plain one.
	readOwned  []ownedRead
	plainReads []string
}

type ownedRead struct {
	path string
	uid  uint32
}

func (h *setupHost) secretsDir() string { return h.path("secrets") }
func (h *setupHost) agentD() string     { return h.path("agent.d") }
func (h *setupHost) stateDir() string   { return h.path("state") }
func (h *setupHost) dropIns() string    { return h.path("dropin") }

// newSetupHost: C with the repository base and the secret pg, K, S, the
// fake restic, systemd (present, active), the system log, alice running sudo.
func newSetupHost(t *testing.T) *setupHost {
	t.Helper()
	h := &setupHost{repoHost: newRepoHost(t), fsys: newFakeFS(), sd: &fakeSystemd{present: true, active: true}, log: &fakeSyslog{}, stdin: &bytes.Buffer{}}
	h.env = map[string]string{"SUDO_USER": "alice", "SUDO_UID": "1000"}
	h.people = []hostsetup.User{svcUser, backupUser, {Name: "root"}}
	h.write(h.path("secrets/base.pass"), passMarker+"\n", 0o600)
	h.write(h.path("secrets/pg"), "pg-value", 0o600)
	h.cfg.Repositories = []config.Repository{{Name: "base", URL: h.path("base"), PasswordFile: h.path("secrets/base.pass")}}
	h.cfg.Secrets = map[string]string{"pg": h.path("secrets/pg")}
	h.cfg.Executor.StateDir = h.stateDir()
	h.cfg.Restic.Path = h.path("restic")
	h.saveConfig()
	h.deps.defaultConfig = h.cfgPath
	h.deps.euid = 0
	h.deps.fs = h.fsys
	h.deps.systemd = h.sd
	h.deps.openAudit = h.log.open
	h.deps.dropInDir = h.dropIns()
	h.deps.stat = h.fsys.ownerStat(serviceUID)
	h.deps.getenv = func(k string) string { return h.env[k] }
	h.deps.processUser = func() (string, uint32) { return "root", 0 }
	h.deps.stdin = h.stdin
	h.deps.readFile = func(p string) ([]byte, error) { h.plainReads = append(h.plainReads, p); return os.ReadFile(p) }
	h.deps.readOwned = func(p string, uid uint32) ([]byte, error) {
		h.readOwned = append(h.readOwned, ownedRead{p, uid})
		return os.ReadFile(p)
	}
	h.deps.terminal = func(io.Writer) hostsetup.Terminal { return nil }
	h.deps.openFile = func(p string) (io.ReadCloser, error) { return os.Open(p) }
	h.deps.lookupUser = func(name string) (hostsetup.User, error) { return users(h.people...)(name) }
	return h
}

// runAs runs a command line "sard-agent <args>" with the process euid.
func (h *setupHost) runAs(euid uint32, args ...string) (code int, stdout, stderr string) {
	h.deps.euid = euid
	var out, errOut bytes.Buffer
	ctx := context.Background()
	args = h.subst(args)
	switch args[0] {
	case "secret":
		code = runSecretWithDeps(ctx, args[1:], &out, &errOut, h.deps)
	default:
		code = runRepoWithDeps(ctx, args[1:], &out, &errOut, h.deps)
	}
	return code, out.String(), errOut.String()
}

// sudo is the command as root.
func (h *setupHost) sudo(args ...string) (int, string, string) { return h.runAs(0, args...) }

// asService is the command as the service user.
func (h *setupHost) asService(args ...string) (int, string, string) {
	return h.runAs(serviceUID, args...)
}

// asOther is the command as a user who is neither root nor the service user.
func (h *setupHost) asOther(args ...string) (int, string, string) {
	return h.runAs(aliceUID, args...)
}

// stdinIs is the standard input of the next command.
func (h *setupHost) stdinIs(s string) { h.stdin.Reset(); h.stdin.WriteString(s) }

// terminalIs makes the standard input a terminal that answers with answers.
func (h *setupHost) terminalIs(answers ...string) *fakeTerminal {
	h.term = &fakeTerminal{answers: answers}
	h.deps.terminal = func(io.Writer) hostsetup.Terminal { return h.term }
	return h.term
}

// hostTree is the host as the tests compare it.
func (h *setupHost) hostTree() map[string]string {
	h.t.Helper()
	got, err := tree(h.dir, h.fsys)
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

// assertHostUnchanged is "Хост не изменён".
func (h *setupHost) assertHostUnchanged(before map[string]string) {
	h.t.Helper()
	if d := diff(before, h.hostTree()); len(d) > 0 {
		h.t.Fatalf("the host changed: %v", d)
	}
	if h.sd.touched() {
		h.t.Fatalf("systemctl was called: %v", h.sd.calls)
	}
	if len(h.log.lines) != 0 {
		h.t.Fatalf("audit lines: %q", h.log.lines)
	}
}

// assertApplied is "Изменение применено": the service was restarted.
func (h *setupHost) assertApplied(stdout string) {
	h.t.Helper()
	if !h.sd.restarted() || !strings.Contains(stdout, "restarted") {
		h.t.Fatalf("the change was not applied: calls %v, stdout %q", h.sd.calls, stdout)
	}
}

func (h *setupHost) assertOwner(path string, uid int) {
	h.t.Helper()
	if o, ok := h.fsys.ownerOf(path); !ok || o.uid != uid {
		h.t.Fatalf("%s: owner %+v (recorded %v), want uid %d", path, o, ok, uid)
	}
}

func (h *setupHost) assertMode(path string, mode os.FileMode) {
	h.t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		h.t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		h.t.Fatalf("%s: mode %v, want %v", path, info.Mode().Perm(), mode)
	}
}

func (h *setupHost) fileContent(path string) string {
	h.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

func (h *setupHost) assertAbsent(paths ...string) {
	h.t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			h.t.Fatalf("%s exists", p)
		}
	}
}

// assertNoSecretLeft is "значения не раскрыты" for the fragments and the audit too.
func (h *setupHost) assertValuesHidden(outputs ...string) {
	h.t.Helper()
	outputs = append(outputs, h.log.lines...)
	for _, dir := range []string{h.agentD()} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			outputs = append(outputs, string(data))
		}
	}
	for _, out := range outputs {
		for _, marker := range []string{"SECRET-MARKER", passMarker, urlMarker} {
			if strings.Contains(out, marker) {
				h.t.Fatalf("%s is exposed:\n%s", marker, out)
			}
		}
	}
}

// base is the repository base of C in the fake restic.
func (h *setupHost) base() *fakeRepo { return h.restic.repo(h.path("base")) }

// holdConfigLock is another command holding the lock of config changes.
func (h *setupHost) holdConfigLock() func() {
	h.t.Helper()
	ok(h.t, os.MkdirAll(h.agentD(), 0o750))
	unlock, err := repoinit.Lock(os.OpenFile, filepath.Join(h.agentD(), ".sard-config.lock"))
	if err != nil {
		h.t.Fatal(err)
	}
	return unlock
}

type secretsInfo = secrets.Info

// keepsTheCaller runs the real restic as the user of the test, after
// checking that the command asked for the service user: a test is not root
// and cannot become another user.
type keepsTheCaller struct {
	restic.ProcessExecutor
	t    *testing.T
	want restic.RunAs
}

func (e keepsTheCaller) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if cmd.RunAs == nil || *cmd.RunAs != e.want {
		e.t.Errorf("restic %v ran as %+v, want %+v", cmd.Args, cmd.RunAs, e.want)
	}
	cmd.RunAs = nil
	return e.ProcessExecutor.Run(ctx, cmd)
}
