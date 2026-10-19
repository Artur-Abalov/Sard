// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// The edges of sard-agent check: what each line says and what each branch of
// the run decides, asserted whole.

const needsRootNote = "some checks need root: run sudo sard-agent check\n"

func TestOnlyTheWordCheckIsTheCheckCommand(t *testing.T) {
	for args, want := range map[string]bool{"check": true, "check --json": true, "repo": false, "": false} {
		if got := isCheckCommand(strings.Fields(args)); got != want {
			t.Errorf("%q: %v", args, got)
		}
	}
}

func TestAMissingServiceUserIsNamedAsTheConfigSetsIt(t *testing.T) {
	h := fullHost(t)
	h.cfg.Service.User = "ghost"
	h.saveHostConfig()
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "service.user", "-"); l.status != "ERROR" || l.message != `the service user "ghost" cannot be found on this host` {
		t.Errorf("%+v", l)
	}
}

// secretOfPgIsRefused makes the secret pg a file the service user is refused.
func (h *setupHost) secretOfPgIsRefused() {
	owned := h.deps.readOwned
	h.deps.readOwned = func(p string, uid uint32) ([]byte, error) {
		if p == h.path("secrets/pg") {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return owned(p, uid)
	}
}

func TestAFileOfARefusedSecretIsAMatterOfRootAndTheHostSaysSoAtTheEnd(t *testing.T) {
	h := fullHost(t)
	h.secretOfPgIsRefused()
	_, stdout, _ := h.asService("check", "--config", "C")
	if !strings.HasSuffix(stdout, "\n"+needsRootNote) {
		t.Errorf("stdout ends with %q", stdout[max(0, len(stdout)-120):])
	}
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	if l := line(t, lines, "secret.readable", "pg"); l.status != "SKIP" || l.message != "needs root: cannot read "+h.path("secrets/pg") {
		t.Errorf("%+v", l)
	}
}

func TestInJSONModeTheNoteAboutRootIsNotPrinted(t *testing.T) {
	h := fullHost(t)
	h.secretOfPgIsRefused()
	_, stdout, _ := h.asService("check", "--config", "C", "--json")
	if strings.Contains(stdout, "some checks need root") || !strings.Contains(stdout, `"needs root`) {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAFileThatIsNotThereIsNoMatterOfRoot(t *testing.T) {
	h := fullHost(t)
	plain := h.deps.readFile
	h.deps.readFile = func(p string) ([]byte, error) {
		if p == h.cfg.TLS.KeyFile {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
		}
		return plain(p)
	}
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	if l := line(t, lines, "tls.identity", "-"); l.status != "ERROR" || strings.Contains(l.message, "needs root") {
		t.Errorf("%+v", l)
	}
}

func TestRootRefusedAFileIsAnErrorNotAMatterOfRoot(t *testing.T) {
	h := fullHost(t)
	plain := h.deps.readFile
	h.deps.readFile = func(p string) ([]byte, error) {
		if p == h.cfg.TLS.KeyFile {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return plain(p)
	}
	_, stdout, _ := h.sudo("check", "--config", "C")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "tls.identity", "-"); l.status != "ERROR" || strings.Contains(l.message, "needs root") {
		t.Errorf("%+v", l)
	}
	if strings.Contains(stdout, "some checks need root") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestARefusalOfOneCheckDoesNotSpreadToTheNext(t *testing.T) {
	h := fullHost(t)
	plain := h.deps.readFile
	h.deps.readFile = func(p string) ([]byte, error) {
		if p == h.cfg.TLS.KeyFile {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return plain(p)
	}
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	for _, id := range []string{"tls.chain", "tls.expiry"} {
		if l := line(t, lines, id, "-"); l.status != "OK" {
			t.Errorf("%+v", l)
		}
	}
}

func TestWithoutAServiceUserNothingIsRunAsItAndTheSecretsAreNotChecked(t *testing.T) {
	h := fullHost(t)
	h.people = h.people[2:] // root only
	lines, _, _ := h.checkLines()
	l := line(t, lines, "secret.readable", "pg")
	if l.status != "SKIP" || !strings.Contains(l.message, "service.user") {
		t.Errorf("%+v", l)
	}
	for _, c := range h.progs.calls {
		if c.runAs != nil {
			t.Errorf("%s ran as %+v", c.key, c.runAs)
		}
	}
}

func TestEverySecretIsLeftOutWithoutAServiceUser(t *testing.T) {
	h := fullHost(t)
	h.cfg.Secrets["pg2"] = h.path("secrets/pg2")
	h.write(h.path("secrets/pg2"), "x", 0o600)
	h.saveHostConfig()
	h.people = h.people[2:]
	lines, _, _ := h.checkLines()
	for _, name := range []string{"pg", "pg2"} {
		if l := line(t, lines, "secret.readable", name); l.status != "SKIP" {
			t.Errorf("%s: %+v", name, l)
		}
	}
}

func TestThePasswordFileOfARepositoryIsAFileOfTheServiceAndItsPermissionsAreJudged(t *testing.T) {
	h := fullHost(t)
	pass := h.path("secrets/base.pass")
	ok(t, os.Chmod(pass, 0o644))
	lines, _, _ := h.checkLines()
	l := line(t, lines, "files.permissions", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, pass) {
		t.Errorf("%+v", l)
	}
}

func TestTheKeyFileIsJudgedOnceAndItsDirectoryWithIt(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.tlsDir(), 0o755))
	lines, _, _ := h.checkLines()
	l := line(t, lines, "files.permissions", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, h.tlsDir()+" has mode 0755") {
		t.Errorf("%+v", l)
	}
}

func TestWithoutAKeyFileThereIsNoDirectoryOfItToJudge(t *testing.T) {
	h := fullHost(t)
	h.cfg.TLS.KeyFile = ""
	h.saveHostConfig()
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "files.permissions", "-"); strings.Contains(l.message, "has mode") {
		t.Errorf("%+v", l)
	}
}

func TestACountOfFragmentsIsWhatTheConfigLoadSays(t *testing.T) {
	h := fullHost(t)
	h.writeFragment("repo-two.yaml", "repositories:\n  - {name: two, url: /x, password_file: /y}\n", 0o640)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "config.load", "-"); l.message != h.cfgPath+" and 1 fragments of agent.d are read" {
		t.Errorf("%+v", l)
	}
}

func TestAConfigThatDoesNotLoadSaysZeroFragments(t *testing.T) {
	h := fullHost(t)
	h.write(h.cfgPath, "server: [", 0o640)
	lines, _, _ := h.checkLines()
	l := line(t, lines, "config.load", "-")
	if l.status != "ERROR" || strings.Contains(l.message, "fragment") {
		t.Errorf("%+v", l)
	}
}

func TestRestAndStateDirOfABrokenConfigAreTakenFromWhatCanBeRead(t *testing.T) {
	h := fullHost(t)
	h.writeFragment("repo-base.yaml", "repositories:\n  - {name: base, url: /x, password_file: /y}\n", 0o640) // a duplicate name
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "restic.binary", "-"); l.status != "OK" || !strings.Contains(l.message, h.cfg.Restic.Path) {
		t.Errorf("restic.binary %+v", l)
	}
	journal := filepath.Join(h.stateDir(), "journal")
	h.write(filepath.Join(journal, "aa.json"), "{}", 0o600)
	lines, _, _ = h.checkLines()
	if l := line(t, lines, "service.steps", "-"); l.message != "1 steps in progress" {
		t.Errorf("service.steps %+v", l)
	}
}

func TestAConfigOfAnotherServiceUserThatTheServiceCannotReadIsAnErrorOfRootsCheck(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.cfgPath, 0o600))
	h.fsys.setOwner(h.cfgPath, 0, 0)
	lines, _, _ := h.checkLines()
	l := line(t, lines, "config.load", "-")
	if l.status != "ERROR" || !strings.Contains(l.message, h.cfgPath) || !strings.Contains(l.message, "the service user sard-agent cannot read it") {
		t.Errorf("%+v", l)
	}
}

func TestTheServiceUserIsNotJudgedAgainstItsOwnRights(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.cfgPath, 0o600))
	h.fsys.setOwner(h.cfgPath, 0, 0)
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	if l := line(t, lines, "config.load", "-"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestProgramsAreCutOffAfterThirtySeconds(t *testing.T) {
	h := fullHost(t)
	rec := &deadlineRecorder{inner: h.progs}
	h.deps.progExec = rec
	h.check()
	if len(rec.left) == 0 {
		t.Fatal("no program was run")
	}
	for _, d := range rec.left {
		if d <= 29*time.Second || d > 30*time.Second {
			t.Errorf("a program was given %v", d)
		}
	}
}

// deadlineRecorder notes how long each run of a program may take.
type deadlineRecorder struct {
	inner interface {
		Run(context.Context, restic.Command) (int, error)
	}
	left []time.Duration
}

func (d *deadlineRecorder) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if at, ok := ctx.Deadline(); ok {
		d.left = append(d.left, time.Until(at))
	} else {
		d.left = append(d.left, -1)
	}
	return d.inner.Run(ctx, cmd)
}

func TestAConfigThatDoesNotLoadForAnotherReasonIsAnErrorForTheServiceUserToo(t *testing.T) {
	h := fullHost(t)
	h.write(h.cfgPath, "server: [", 0o640)
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	if l := line(t, lines, "config.load", "-"); l.status != "ERROR" || strings.Contains(l.message, "needs root") {
		t.Errorf("%+v", l)
	}
}

func TestRootRefusedTheConfigIsAnErrorOfTheLoad(t *testing.T) {
	h := fullHost(t)
	h.deps.loadConfig = func(string) (config.Config, error) {
		return config.Config{}, &fs.PathError{Op: "open", Path: h.agentD(), Err: fs.ErrPermission}
	}
	_, stdout, _ := h.sudo("check", "--config", "C")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "config.load", "-"); l.status != "ERROR" || strings.Contains(l.message, "needs root") {
		t.Errorf("%+v", l)
	}
	if strings.Contains(stdout, "some checks need root") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestWhenTheServiceUserCannotReadTheConfigWhatNeedsItIsNotChecked(t *testing.T) {
	h := fullHost(t)
	h.deps.loadConfig = func(string) (config.Config, error) {
		return config.Config{}, &fs.PathError{Op: "open", Path: h.agentD(), Err: fs.ErrPermission}
	}
	_, stdout, _ := h.asService("check", "--config", "C")
	if !strings.HasSuffix(stdout, "\n"+needsRootNote) {
		t.Errorf("stdout %q", stdout)
	}
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	for _, id := range []string{"config.names", "config.references", "files.permissions"} {
		if l := line(t, lines, id, "-"); l.status != "SKIP" || !strings.Contains(l.message, "config.load") {
			t.Errorf("%s: %+v", id, l)
		}
	}
}

func TestWithoutAServiceUserTheConfigIsNotJudgedByItsRights(t *testing.T) {
	h := fullHost(t)
	h.people = []hostsetup.User{{Name: "root"}}
	stat := h.deps.stat
	h.deps.stat = func(p string) (secrets.Info, error) {
		info, err := stat(p)
		if p == h.cfgPath {
			info.Mode, info.UID, info.GID = 0o044, 0, 0
		}
		return info, err
	}
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "config.load", "-"); l.status != "OK" {
		t.Errorf("%+v", l)
	}
}

func TestAFragmentThatDoesNotLoadStillLetsResticBeFoundByTheMainFile(t *testing.T) {
	h := fullHost(t)
	h.writeFragment("x.yaml", "server:\n  address: x:1\n", 0o640)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "config.load", "-"); l.status != "ERROR" {
		t.Errorf("config.load %+v", l)
	}
	if l := line(t, lines, "restic.binary", "-"); l.status != "OK" || !strings.Contains(l.message, h.cfg.Restic.Path) {
		t.Errorf("restic.binary %+v", l)
	}
}

func TestADuplicateNameIsAnErrorOfTheNamesAndNotOfTheLoad(t *testing.T) {
	h := fullHost(t)
	h.writeFragment("repo-base.yaml", "repositories:\n  - {name: base, url: /x, password_file: /y}\n", 0o640)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "config.load", "-"); l.status != "OK" || l.message != h.cfgPath+" and 1 fragments of agent.d are read" {
		t.Errorf("config.load %+v", l)
	}
	if l := line(t, lines, "config.names", "-"); l.status != "ERROR" {
		t.Errorf("config.names %+v", l)
	}
}

// onReads makes the reads of one file fail as fail says, by their number from 1.
func (h *setupHost) onReads(path string, fail func(n int) ([]byte, error)) {
	plain := h.deps.readFile
	n := 0
	h.deps.readFile = func(p string) ([]byte, error) {
		if p == path {
			n++
			if data, err := fail(n); data != nil || err != nil {
				return data, err
			}
		}
		return plain(p)
	}
}

func refusedRead(path string) error {
	return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
}

func TestAKeyThatTheServiceUserIsRefusedOnlyWhenTheServerIsAskedIsAMatterOfRootForTheServerChecks(t *testing.T) {
	h := fullHost(t)
	key := h.cfg.TLS.KeyFile
	h.onReads(key, func(n int) ([]byte, error) {
		if n > 1 { // the identity read it, the connection to the server may not
			return nil, refusedRead(key)
		}
		return nil, nil
	})
	_, stdout, _ := h.asService("check", "--config", "C")
	if !strings.HasSuffix(stdout, "\n"+needsRootNote) {
		t.Errorf("stdout %q", stdout)
	}
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	for _, id := range serverChecks {
		if l := line(t, lines, id, "-"); l.status != "SKIP" || l.message != "needs root: cannot read "+key {
			t.Errorf("%s: %+v", id, l)
		}
	}
}

func TestACertificateAndKeyThatDoNotMatchWhenTheServerIsAskedAreWhyTLSCannotBeBuilt(t *testing.T) {
	h := fullHost(t)
	key := h.cfg.TLS.KeyFile
	other := h.certHolding(time.Hour).key
	h.onReads(key, func(n int) ([]byte, error) {
		if n > 1 {
			return other, nil
		}
		return nil, nil
	})
	h.checkLines()
	err := h.server.calls[0].TLSErr
	if err == nil || !strings.HasPrefix(err.Error(), "tls.cert_file, tls.key_file: ") {
		t.Errorf("%v", err)
	}
}

func TestAMissingCAFileIsReportedToTheProbeWithTheKeyAndTheCause(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Remove(h.cfg.TLS.CAFile))
	h.checkLines()
	err := h.server.calls[0].TLSErr
	if err == nil || !strings.HasPrefix(err.Error(), "tls.ca_file: open "+h.cfg.TLS.CAFile) {
		t.Errorf("%v", err)
	}
}

func TestAFileThatIsNotThereIsNoMatterOfRootForTheServerChecks(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Remove(h.cfg.TLS.CAFile))
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	for _, id := range serverChecks {
		if l := line(t, lines, id, "-"); strings.Contains(l.message, "needs root") {
			t.Errorf("%s: %+v", id, l)
		}
	}
	if h.server.count() != 1 {
		t.Errorf("the server was asked %d times", h.server.count())
	}
}

func TestARefusalOfTheLastCheckBeforeTheServerIsNotTakenForTheRefusalOfTheServerChecks(t *testing.T) {
	h := fullHost(t)
	cert := h.cfg.TLS.CertFile
	h.onReads(cert, func(n int) ([]byte, error) {
		if n == 3 { // identity, chain, and then only the expiry is refused
			return nil, refusedRead(cert)
		}
		return nil, nil
	})
	ok(t, os.Remove(h.cfg.TLS.CAFile))
	_, stdout, _ := h.asService("check", "--config", "C")
	lines, _ := parseOut(t, strings.ReplaceAll(stdout, needsRootNote, ""))
	if l := line(t, lines, "tls.expiry", "-"); l.status != "SKIP" {
		t.Fatalf("tls.expiry %+v", l)
	}
	for _, id := range serverChecks {
		if l := line(t, lines, id, "-"); strings.Contains(l.message, "needs root") {
			t.Errorf("%s: %+v", id, l)
		}
	}
}

func TestTheNameOfTheServerIsTheWholeAddressWhenItHasNoPort(t *testing.T) {
	h := fullHost(t)
	h.cfg.Server.Address = "sard.example.org"
	h.saveHostConfig()
	h.checkLines()
	if r := h.server.calls[0]; r.TLS == nil || r.TLS.ServerName != "sard.example.org" {
		t.Errorf("%+v", r.TLS)
	}
}

// --- restic.binary

// versionHook runs a hook at every restic version, by its number from 1.
type versionHook struct {
	inner restic.Executor
	n     int
	on    func(n int) (code int, err error, handled bool)
}

func (v *versionHook) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if len(cmd.Args) > 0 && cmd.Args[0] == "version" {
		v.n++
		if code, err, handled := v.on(v.n); handled {
			return code, err
		}
	}
	return v.inner.Run(ctx, cmd)
}

func TestAResticThatIsNotThereIsNamedByItsFailure(t *testing.T) {
	h := fullHost(t)
	h.cfg.Restic.Path = h.path("missing-restic")
	h.saveHostConfig()
	lines, _, _ := h.checkLines()
	l := line(t, lines, "restic.binary", "-")
	if l.status != "ERROR" || !strings.HasPrefix(l.message, "RESTIC_NOT_FOUND: restic was not found at "+h.path("missing-restic")) {
		t.Errorf("%+v", l)
	}
}

func TestAResticThatCannotAnswerBecauseOfAnInterruptIsUnusable(t *testing.T) {
	h := fullHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	hook := &versionHook{inner: h.restic, on: func(int) (int, error, bool) { cancel(); return -1, ctx.Err(), true }}
	h.deps.exec = hook
	var out, errOut strings.Builder
	h.deps.euid = 0
	runCheck(ctx, h.subst([]string{"--config", "C"}), &out, &errOut, h.deps)
	lines, _ := parseOut(t, out.String())
	l := line(t, lines, "restic.binary", "-")
	if l.status != "ERROR" || !strings.HasPrefix(l.message, "RESTIC_UNUSABLE: ") || !strings.Contains(l.message, "context canceled") || hook.n != 1 {
		t.Errorf("%+v after %d versions", l, hook.n)
	}
}

func TestAResticThatAnswersTheCheckButNotTheVersionIsUnusable(t *testing.T) {
	h := fullHost(t)
	h.deps.exec = &versionHook{inner: h.restic, on: func(n int) (int, error, bool) { return 1, nil, n == 2 }}
	lines, _, _ := h.checkLines()
	l := line(t, lines, "restic.binary", "-")
	if l.status != "ERROR" || !strings.HasPrefix(l.message, "RESTIC_UNUSABLE: ") {
		t.Errorf("%+v", l)
	}
}

// --- plugin programs

func TestAnEmptyEntryOfThePathIsNotTheCurrentDirectory(t *testing.T) {
	h := fullHost(t)
	h.deps.pathEnv = ":/usr/local/bin::" + pgBin
	h.progs.answer("pg_dump --version", pgVersionLine("pg_dump", "9.9"))
	lines, _, _ := h.checkLines()
	l := line(t, lines, "plugin.programs", "postgresql")
	if l.status != "OK" || strings.Contains(l.message, "9.9") || !strings.Contains(l.message, "pg_dump /usr/bin/pg_dump 16.4") {
		t.Errorf("%+v", l)
	}
}

func TestOnlyTheFirstDirectoryWithPgDumpIsLookedAtForTheTools(t *testing.T) {
	h := fullHost(t)
	h.installPostgres("/opt/pg15/bin", "15.2")
	h.deps.pathEnv = pgBin + ":/opt/pg15/bin"
	lines, _, _ := h.checkLines()
	l := line(t, lines, "plugin.programs", "postgresql")
	if l.status != "OK" || strings.Contains(l.message, "/opt/pg15") {
		t.Errorf("%+v", l)
	}
	if n := h.progs.ran("/opt/pg15/bin/"); n != 0 {
		t.Errorf("%d programs of the second directory were run", n)
	}
}

func TestPgDumpIsRunOnceAndAMissingToolIsNamed(t *testing.T) {
	h := fullHost(t)
	h.progs.remove(pgBin + "/psql --version")
	lines, _, _ := h.checkLines()
	l := line(t, lines, "plugin.programs", "postgresql")
	if l.status != "ERROR" || !strings.Contains(l.message, "psql is not beside pg_dump") {
		t.Errorf("%+v", l)
	}
	if n := h.progs.ran(pgBin + "/pg_dump --version"); n != 1 {
		t.Errorf("pg_dump was run %d times", n)
	}
}

// --- SELinux

func TestWithABrokenConfigTheLabelsAreNotChecked(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	h.write(h.cfgPath, "server: [", 0o640)
	lines, _, _ := h.checkLines()
	l := line(t, lines, "selinux.labels", "-")
	if l.status != "SKIP" || !strings.Contains(l.message, "config.load") {
		t.Errorf("%+v", l)
	}
	if h.progs.ran("matchpathcon") != 0 {
		t.Error("matchpathcon was run")
	}
}

func matchpathconArgs(t *testing.T, h *setupHost) []string {
	t.Helper()
	for _, k := range h.progs.keys() {
		if strings.HasPrefix(k, "matchpathcon -V ") {
			return strings.Fields(k)[2:]
		}
	}
	t.Fatalf("matchpathcon was not run: %v", h.progs.keys())
	return nil
}

func TestTheLabelsOfTheFilesOfEveryRepositoryAndSecretAreAsked(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	ok(t, os.MkdirAll(h.path("base"), 0o700))
	h.check()
	args := matchpathconArgs(t, h)
	for _, want := range []string{h.path("secrets/base.pass"), h.path("secrets/pg"), h.path("base"), h.cfg.TLS.KeyFile, h.cfg.TLS.CAFile} {
		if !slices.Contains(args, want) {
			t.Errorf("%v lacks %s", args, want)
		}
	}
	for i, a := range args {
		if slices.Contains(args[:i], a) {
			t.Errorf("%s is asked twice: %v", a, args)
		}
	}
}

func TestTheLabelOfAFileIsAskedOnceWhenTwoSettingsNameIt(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	h.cfg.TLS.CAFile = h.cfg.TLS.CertFile
	h.saveHostConfig()
	h.check()
	args := matchpathconArgs(t, h)
	n := 0
	for _, a := range args {
		if a == h.cfg.TLS.CertFile {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%s is asked %d times: %v", h.cfg.TLS.CertFile, n, args)
	}
}

func TestADenialAtTheEdgeOfTheLast24HoursIsCountedAndOneBeyondIsNot(t *testing.T) {
	h := fullHost(t)
	h.onSELinux()
	in := avcAt("restic", 1, h.clock.now.Add(-23*time.Hour-30*time.Minute).Unix())
	out := avcAt("restic", 2, h.clock.now.Add(-24*time.Hour-30*time.Minute).Unix())
	h.progs.prefix("ausearch -m avc -ts yesterday", in+"\n----\n"+out+"\n")
	lines, _, _ := h.checkLines()
	l := line(t, lines, "selinux.denials", "-")
	if l.status != "WARN" || !strings.Contains(l.message, "1 denials") || !strings.Contains(l.message, "pg1") || strings.Contains(l.message, "pg2") {
		t.Errorf("%+v", l)
	}
}

// --- the service

func TestWithoutAJournalThereAreNoStepsInProgress(t *testing.T) {
	h := fullHost(t)
	ok(t, os.RemoveAll(filepath.Join(h.stateDir(), "journal")))
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "service.steps", "-"); l.status != "OK" || l.message != "no steps in progress" {
		t.Errorf("%+v", l)
	}
}

func TestOnlyTheEntriesOfTheJournalThatAreStepsAreCounted(t *testing.T) {
	h := fullHost(t)
	journal := filepath.Join(h.stateDir(), "journal")
	for _, name := range []string{"aa.json", "notes.txt", ".tmp-bb.json"} {
		h.write(filepath.Join(journal, name), "{}", 0o600)
	}
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "service.steps", "-"); l.message != "1 steps in progress" {
		t.Errorf("%+v", l)
	}
}

func TestASystemctlThatDoesNotAnswerIsAWarning(t *testing.T) {
	h := fullHost(t)
	h.progs.remove(showKey)
	lines, _, code := h.checkLines()
	assertCode(t, code, exitWarnings)
	l := line(t, lines, "service.running", "-")
	if l.status != "WARN" || !strings.HasPrefix(l.message, "systemctl show did not answer: ") {
		t.Errorf("%+v", l)
	}
}

func TestAnAnswerOfSystemctlThatIsNoAnswerIsAWarning(t *testing.T) {
	h := fullHost(t)
	h.progs.answer(showKey, "ActiveState=active\nNRestarts=many\n")
	lines, _, code := h.checkLines()
	assertCode(t, code, exitWarnings)
	l := line(t, lines, "service.running", "-")
	if l.status != "WARN" || !strings.Contains(l.message, "NRestarts") {
		t.Errorf("%+v", l)
	}
}
