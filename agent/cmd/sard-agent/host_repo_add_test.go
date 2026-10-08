// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// extraDir is D/repo-extra, the local repository of the scenarios.
func (h *setupHost) extraDir() string { return h.path("repo-extra") }

// add is "repo add extra D/repo-extra".
func (h *setupHost) add(extra ...string) (int, string, string) {
	args := append([]string{"repo", "add", "extra", h.extraDir(), "--config", "C"}, extra...)
	return h.sudo(args...)
}

// existingRepository is a repository in D/repo-extra opened by password.
func (h *setupHost) existingRepository(password string) *fakeRepo {
	h.t.Helper()
	ok(h.t, os.MkdirAll(h.extraDir(), 0o755))
	r := h.restic.repo(h.extraDir())
	r.initialized, r.password = true, password
	return r
}

var base64URL43 = regexp.MustCompile(`^[A-Za-z0-9_-]{43}\n$`)

// Rule "repo add подключает локальное хранилище одной командой".

func TestAnEmptyRepositoryIsCreatedWithAGeneratedPassword(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.add()
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertOwner(h.extraDir(), serviceUID)
	h.assertMode(h.extraDir(), 0o700)
	pass := h.path("secrets/restic-extra.pass")
	h.assertOwner(pass, serviceUID)
	h.assertMode(pass, 0o600)
	if !base64URL43.MatchString(h.fileContent(pass)) {
		t.Fatalf("password file %q", h.fileContent(pass))
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestResticOpensTheNewRepositoryWithTheGeneratedPasswordFile(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	for _, c := range h.restic.callsTo(h.extraDir(), "cat") {
		if c.passwordFile == "" || c.password == "" {
			t.Fatalf("restic cat ran without the password: %+v", c)
		}
	}
	pass := h.path("secrets/restic-extra.pass")
	if got := h.restic.callsTo(h.extraDir(), "init"); len(got) != 1 || got[0].passwordFile != pass {
		t.Fatalf("restic init: %+v", got)
	}
}

func TestTheFragmentOfANewRepositoryIsWrittenAndTheMainConfigIsNot(t *testing.T) {
	h := newSetupHost(t)
	mainBefore := h.fileContent(h.cfgPath)
	h.add()
	fragment := h.fileContent(h.path("agent.d/repo-extra.yaml"))
	for _, want := range []string{"name: extra", "url: " + h.extraDir(), "password_file: " + h.path("secrets/restic-extra.pass")} {
		if !strings.Contains(fragment, want) {
			t.Errorf("fragment lacks %q:\n%s", want, fragment)
		}
	}
	if h.fileContent(h.cfgPath) != mainBefore {
		t.Fatal("the main config changed")
	}
}

func TestTheSummaryNamesTheRepositoryAndAsksForACopyOfThePassword(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.add()
	assertCode(t, code, exitOK)
	for _, want := range []string{"extra", "local", h.extraDir(), "created a new repository", "only on this host", "unrecoverable", "sudo sard-agent repo password extra --reveal"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if id := hexID.FindString(stdout); id != h.restic.repo(h.extraDir()).id {
		t.Errorf("repository_id %q in %s", id, stdout)
	}
	if strings.Contains(stdout, "restic init") {
		t.Errorf("stdout %q", stdout)
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestALocalRepositoryWarnsThatTheBackupIsLostWithTheHost(t *testing.T) {
	h := newSetupHost(t)
	_, stdout, _ := h.add()
	if !strings.Contains(stdout, "lost together with this host") {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestMissingParentDirectoriesAreCreatedForRoot(t *testing.T) {
	h := newSetupHost(t)
	target := h.path("a/b/repo")
	code, _, _ := h.sudo("repo", "add", "extra", target, "--config", "C")
	assertCode(t, code, exitOK)
	for _, dir := range []string{h.path("a"), h.path("a/b")} {
		if o, _ := h.fsys.ownerOf(dir); o != (ownerRec{0, 0}) {
			t.Errorf("%s owner %+v", dir, o)
		}
		h.assertMode(dir, 0o755)
	}
	h.assertOwner(target, serviceUID)
	h.assertMode(target, 0o700)
}

func TestARepositoryDirectoryThatCannotBeCreatedIsAWriteErrorWithoutTraces(t *testing.T) {
	h := newSetupHost(t)
	h.fsys.failOn, h.fsys.failPath = "mkdir", "repo-extra"
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	if !strings.Contains(stderr, h.extraDir()) {
		t.Fatalf("stderr %q", stderr)
	}
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 {
		t.Fatalf("restic init was called %d times", n)
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestAnExistingEmptyDirectoryBecomesTheServiceUsers(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.Mkdir(h.extraDir(), 0o755))
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	h.assertOwner(h.extraDir(), serviceUID)
}

func TestAnExistingRepositoryIsAttachedByItsPasswordFromStandardInput(t *testing.T) {
	h := newSetupHost(t)
	r := h.existingRepository(passMarker)
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.add("--password-stdin")
	assertCode(t, code, exitOK)
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 {
		t.Fatalf("restic init was called %d times", n)
	}
	if !strings.Contains(stdout, "attached an existing repository") || !strings.Contains(stdout, r.id) {
		t.Fatalf("stdout %q", stdout)
	}
	pass := h.path("secrets/restic-extra.pass")
	if h.fileContent(pass) != passMarker+"\n" {
		t.Fatalf("password file %q", h.fileContent(pass))
	}
	h.assertOwner(pass, serviceUID)
	h.assertMode(pass, 0o600)
	h.assertValuesHidden(stdout, stderr)
}

func TestTheOnlyPasswordOfAnExistingRepositoryCanBeTypedAtTheTerminal(t *testing.T) {
	h := newSetupHost(t)
	h.existingRepository(passMarker)
	term := h.terminalIs(passMarker, passMarker)
	code, _, stderr := h.add()
	assertCode(t, code, exitOK)
	if len(term.prompts) != 2 {
		t.Fatalf("prompts %q", term.prompts)
	}
	h.assertValuesHidden(stderr)
}

func TestThePasswordOfAnExistingRepositoryCanBeGivenInAFile(t *testing.T) {
	h := newSetupHost(t)
	h.existingRepository(passMarker)
	src := h.path("outside/F")
	h.write(src, passMarker+"\n", 0o644)
	code, _, _ := h.add("--password-from-file", src)
	assertCode(t, code, exitOK)
}

func TestAnExistingRepositoryWithoutASourceAndATerminalIsRefusedAtOnce(t *testing.T) {
	h := newSetupHost(t)
	h.existingRepository(passMarker)
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_MISSING")
	for _, want := range []string{"--password-stdin", "--password-from-file"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %s: %q", want, stderr)
		}
	}
	if h.hung.reads != 0 {
		t.Fatal("the input was read")
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.path("secrets/restic-extra.pass"))
	h.assertNoTemporaryFiles(h.secretsDir())
}

func TestAWrongPasswordLeavesNoTraces(t *testing.T) {
	h := newSetupHost(t)
	h.existingRepository("another password")
	h.stdinIs(passMarker)
	code, stdout, stderr := h.add("--password-stdin")
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.path("secrets/restic-extra.pass"))
	h.assertNoTemporaryFiles(h.secretsDir())
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 {
		t.Fatalf("restic init was called %d times", n)
	}
	h.assertValuesHidden(stdout, stderr)
}

func (h *setupHost) assertNoTemporaryFiles(dirs ...string) {
	h.t.Helper()
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp-") {
				h.t.Errorf("%s was left in %s", e.Name(), dir)
			}
		}
	}
}

func TestAGivenPasswordCreatesTheEmptyRepository(t *testing.T) {
	h := newSetupHost(t)
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.add("--password-stdin")
	assertCode(t, code, exitOK)
	init := h.restic.callsTo(h.extraDir(), "init")
	if len(init) != 1 || init[0].password != passMarker {
		t.Fatalf("restic init: %+v", init)
	}
	if strings.Contains(stdout, "generated") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestAnEmptyRepositoryDoesNotAskForAPasswordAtTheTerminal(t *testing.T) {
	h := newSetupHost(t)
	term := h.terminalIs()
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	if len(term.prompts) != 0 {
		t.Fatalf("the terminal was asked: %q", term.prompts)
	}
	if !base64URL43.MatchString(h.fileContent(h.path("secrets/restic-extra.pass"))) {
		t.Fatal("no generated password")
	}
}

func TestAnExistingRepositoryOfAnotherOwnerBelongsToTheServiceUserAfterwards(t *testing.T) {
	h := newSetupHost(t)
	h.existingRepository(passMarker)
	ok(t, os.MkdirAll(filepath.Join(h.extraDir(), "data", "00"), 0o755))
	ok(t, os.WriteFile(filepath.Join(h.extraDir(), "config"), []byte("c"), 0o400))
	ok(t, os.WriteFile(filepath.Join(h.extraDir(), "data", "00", "blob"), []byte("b"), 0o444))
	h.stdinIs(passMarker)
	code, _, _ := h.add("--password-stdin")
	assertCode(t, code, exitOK)
	for _, p := range []string{"", "config", "data", "data/00", "data/00/blob"} {
		h.assertOwner(filepath.Join(h.extraDir(), p), serviceUID)
	}
	h.assertMode(filepath.Join(h.extraDir(), "config"), 0o400)
	h.assertMode(filepath.Join(h.extraDir(), "data/00/blob"), 0o444)
}

func TestRepeatingRepoAddChangesNothingAndAsksNothing(t *testing.T) {
	h := newSetupHost(t)
	_, first, _ := h.add()
	id := hexID.FindString(first)
	h.sd.calls, h.log.lines = nil, nil
	h.restic.calls = nil
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	before := h.hostTree()
	code, stdout, _ := h.add()
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") || !strings.Contains(stdout, id) || !strings.Contains(stdout, "only on this host") {
		t.Fatalf("stdout %q", stdout)
	}
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 || h.hung.reads != 0 {
		t.Fatalf("init calls %d, reads %d", n, h.hung.reads)
	}
	h.assertHostUnchanged(before)
}

func TestTheSameNameWithAnotherAddressIsRefused(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	h.sd.calls, h.log.lines = nil, nil
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "add", "extra", h.path("repo-other"), "--config", "C")
	assertRefusal(t, code, stderr, exitIdentityExists, "REPOSITORY_CONFLICT")
	for _, want := range []string{h.extraDir(), "repo remove extra"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	h.assertHostUnchanged(before)
}

func TestAnAddressedNameOfTheMainConfigIsRefused(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "add", "base", h.extraDir(), "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "DEFINED_IN_CONFIG")
	if !strings.Contains(stderr, h.cfgPath) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestOtherBackendsAreAStubInThisSlice(t *testing.T) {
	cases := map[string]string{
		"s3:https://s3.example.com/bucket/extra":                  "s3",
		"sftp:backup@nas.example.com:/extra":                      "sftp",
		"rest:https://u:" + urlMarker + "@rest.example.com/extra": "rest",
		"b2:bucket:extra": "b2",
	}
	for address, kind := range cases {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sudo("repo", "add", "extra", address, "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "BACKEND_NOT_SUPPORTED")
		if !strings.Contains(stderr, kind) || !strings.Contains(stderr, "only a local path") {
			t.Errorf("stderr %q", stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertValuesHidden(stdout, stderr)
	}
}

func TestUnusableLocalPathsAreRefused(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("file"), "x", 0o600)
	for _, path := range []string{"relative-dir", h.path("file")} {
		before := h.hostTree()
		code, _, stderr := h.sudo("repo", "add", "extra", path, "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "LOCAL_PATH_INVALID")
		if !strings.Contains(stderr, path) {
			t.Errorf("stderr lacks the path %q: %s", path, stderr)
		}
		h.assertHostUnchanged(before)
	}
}

func TestMalformedRepoAddCommandLinesAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"repo", "add", "--config", "C"},
		{"repo", "add", "extra", "--config", "C"},
		{"repo", "add", "extra", "/srv/x", "lost", "--config", "C"},
		{"repo", "add", "extra", "/srv/x", "--password-stdin", "--password-from-file", "F", "--config", "C"},
		{"repo", "add", "extra", "/srv/x", "--timeout", "0s", "--config", "C"},
		{"repo", "add", "extra", "/srv/x", "--password", passMarker, "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sudo(args...)
		assertCode(t, code, exitUsage)
		h.assertHostUnchanged(before)
		h.assertValuesHidden(stdout, stderr)
	}
}

func TestAResticRefusalWhileCreatingKeepsThePasswordAndWritesNoFragment(t *testing.T) {
	h := newSetupHost(t)
	h.restic.repo(h.extraDir()).initFatal = "permission denied"
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitAgentError, "BACKEND_REFUSED")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
	pass := h.path("secrets/restic-extra.pass")
	h.assertOwner(pass, serviceUID)
	h.assertMode(pass, 0o600)
	if !strings.Contains(stderr, "password file") || !strings.Contains(stderr, "will be used when the command is repeated") {
		t.Fatalf("stderr %q", stderr)
	}
	if h.sd.touched() {
		t.Fatalf("systemctl: %v", h.sd.calls)
	}
}

func TestRepeatingAfterAFailureUsesTheLeftoverPasswordFile(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("secrets/restic-extra.pass"), "Q\n", 0o600)
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	init := h.restic.callsTo(h.extraDir(), "init")
	if len(init) != 1 || init[0].password != "Q" {
		t.Fatalf("restic init: %+v", init)
	}
	if h.fileContent(h.path("secrets/restic-extra.pass")) != "Q\n" {
		t.Fatal("the leftover password file changed")
	}
}

func TestRepeatingAfterAFailureAttachesTheRepositoryTheLeftoverOpens(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("secrets/restic-extra.pass"), "Q\n", 0o600)
	h.existingRepository("Q")
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 || h.hung.reads != 0 {
		t.Fatalf("init calls %d, reads %d", n, h.hung.reads)
	}
}

func TestALeftoverThatDoesNotOpenTheRepositoryAsksForThePassword(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("secrets/restic-extra.pass"), "stale\n", 0o600)
	h.existingRepository(passMarker)
	h.stdinIs(passMarker)
	code, _, _ := h.add("--password-stdin")
	assertCode(t, code, exitOK)
	if h.fileContent(h.path("secrets/restic-extra.pass")) != passMarker {
		t.Fatalf("password file %q", h.fileContent(h.path("secrets/restic-extra.pass")))
	}
}

func (h *setupHost) startAs(ctx context.Context, args ...string) <-chan cmdResult {
	done := make(chan cmdResult, 1)
	go func() {
		h.deps.euid = 0
		var out, errOut strings.Builder
		code := runRepoWithDeps(ctx, h.subst(args)[1:], &out, &errOut, h.deps)
		done <- cmdResult{code, out.String(), errOut.String()}
	}()
	return done
}

func TestATimeoutOfRepoAddIsTemporaryAndWritesNoFragment(t *testing.T) {
	h := newSetupHost(t)
	r := h.restic.repo(h.extraDir())
	r.hangInit = true
	done := h.startAs(context.Background(), "repo", "add", "extra", h.extraDir(), "--config", "C")
	h.clock.waitTimer(t)
	<-r.initEntered
	h.clock.fireNow()
	res := within(t, done)
	assertRefusal(t, res.code, res.stderr, exitTemporary, "TIMEOUT")
	if !r.terminated {
		t.Error("restic did not get SIGTERM")
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestAnInterruptOfRepoAddIsTemporaryAndWritesNoFragment(t *testing.T) {
	h := newSetupHost(t)
	r := h.restic.repo(h.extraDir())
	r.hangInit = true
	ctx, cancel := context.WithCancel(context.Background())
	done := h.startAs(ctx, "repo", "add", "extra", h.extraDir(), "--config", "C")
	<-r.initEntered
	cancel()
	res := within(t, done)
	assertRefusal(t, res.code, res.stderr, exitTemporary, "INTERRUPTED")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestAnUnusableResticIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Restic.Path = h.path("no-restic")
	h.saveConfig()
	before := h.hostTree()
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitAgentError, "RESTIC_NOT_FOUND")
	h.assertAbsent(h.extraDir())
	h.assertHostUnchanged(before)
}

func TestWithoutTheResticCacheDirectoryRepoAddIsAWriteErrorBeforeTheRepositoryDirectory(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.Remove(h.cacheDir()))
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitWrite, "LOCK_WRITE")
	h.assertAbsent(h.extraDir())
}

func TestAnInitOfTheSameNameInProgressBlocksRepoAdd(t *testing.T) {
	h := newSetupHost(t)
	unlock, err := repoinit.Lock(os.OpenFile, repoinit.LockPath(h.cacheDir(), "extra"))
	ok(t, err)
	defer unlock()
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitTemporary, "INIT_IN_PROGRESS")
}

func TestOnASystemdHostTheServiceMayWriteToTheRepositoryDirectory(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	body := h.fileContent(filepath.Join(h.dropIns(), "sard-repo-extra.conf"))
	if !strings.Contains(body, "ReadWritePaths="+h.extraDir()) {
		t.Fatalf("drop-in %q", body)
	}
	reload, restart := -1, -1
	for i, c := range h.sd.calls {
		if c == "daemon-reload" && reload < 0 {
			reload = i
		}
		if strings.HasPrefix(c, "restart") {
			restart = i
		}
	}
	if reload < 0 || restart < reload {
		t.Fatalf("calls %v", h.sd.calls)
	}
}

func TestWithoutSystemdNoDropInIsMade(t *testing.T) {
	h := newSetupHost(t)
	h.sd.present = false
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	h.assertAbsent(h.dropIns())
}

func TestRepoAddIsAppliedAndAudited(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.add()
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra added") || !strings.Contains(h.log.lines[0], "alice") {
		t.Fatalf("audit %q", h.log.lines)
	}
	h.assertValuesHidden()
}

func TestAnAddedRepositoryIsSeenByTheNextCommands(t *testing.T) {
	h := newSetupHost(t)
	_, first, _ := h.add()
	id := hexID.FindString(first)
	code, stdout, _ := h.sudo("repo", "show", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	if card(stdout)["repository_id"] != id || card(stdout)["defined_in"] != h.path("agent.d/repo-extra.yaml") {
		t.Fatalf("card %v", card(stdout))
	}
}

func TestADropInThatCannotBeWrittenStopsBeforeTheFragment(t *testing.T) {
	h := newSetupHost(t)
	h.fsys.failOn, h.fsys.failPath = "mkdir", "dropin"
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestAFragmentThatCannotBeWrittenAfterTheRepositoryIsMadeIsAWriteError(t *testing.T) {
	h := newSetupHost(t)
	h.fsys.failOn, h.fsys.failPath = "rename", "repo-extra.yaml"
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
	if !strings.Contains(stderr, h.path("agent.d/repo-extra.yaml")) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertNoTemporaryFiles(h.agentD(), h.secretsDir())
}

// Rule "repo remove убирает репозиторий только из настройки хоста".

func TestRemovingARepositoryKeepsTheDataAndThePassword(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	h.sd.calls, h.log.lines = nil, nil
	h.restic.calls = nil
	data := filepath.Join(h.extraDir(), "keep")
	ok(t, os.WriteFile(data, []byte("data"), 0o600))
	code, stdout, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), filepath.Join(h.dropIns(), "sard-repo-extra.conf"))
	if h.sd.calls[0] != "daemon-reload" {
		t.Fatalf("calls %v", h.sd.calls)
	}
	if _, err := os.Stat(h.path("secrets/restic-extra.pass")); err != nil {
		t.Fatal("the password file was removed")
	}
	if h.fileContent(data) != "data" {
		t.Fatal("the data changed")
	}
	if len(h.restic.calls) != 0 {
		t.Fatalf("restic was called: %v", h.restic.subs())
	}
	h.assertApplied(stdout)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra removed") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestTheRemovalSummaryWarnsAboutServerSourcesAndNamesTheRemainingPassword(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	_, stdout, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	for _, want := range []string{"sources on the server", "extra", h.path("secrets/restic-extra.pass"), "not deleted"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestAddingAgainAfterARemovalAsksNoPassword(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	h.sudo("repo", "remove", "extra", "--config", "C")
	h.restic.calls = nil
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	code, _, _ := h.add()
	assertCode(t, code, exitOK)
	if n := len(h.restic.callsTo(h.extraDir(), "init")); n != 0 || h.hung.reads != 0 {
		t.Fatalf("init calls %d, reads %d", n, h.hung.reads)
	}
}

func TestRemovingARepositoryThatIsNotThereDoesNothing(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, stdout, _ := h.sudo("repo", "remove", "nope", "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "nothing to remove") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertHostUnchanged(before)
}

func TestARepositoryOfTheMainConfigIsNotRemoved(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "remove", "base", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "DEFINED_IN_CONFIG")
	if !strings.Contains(stderr, h.cfgPath) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestRemovingKeepsTheEnvFileSomethingElseUsesAndRemovesItsOwn(t *testing.T) {
	h := newSetupHost(t)
	env := h.path("secrets/extra.env")
	h.write(env, "K=V\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), "repositories:\n  - {name: extra, url: "+h.extraDir()+", password_file: "+h.path("secrets/restic-extra.pass")+", env_file: "+env+"}\n", 0o640)
	code, _, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	h.assertAbsent(env)
}

func TestAnEnvFileOutsideTheSecretsDirectoryIsKept(t *testing.T) {
	h := newSetupHost(t)
	env := h.path("elsewhere/extra.env")
	h.write(env, "K=V\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), "repositories:\n  - {name: extra, url: "+h.extraDir()+", password_file: "+h.path("secrets/restic-extra.pass")+", env_file: "+env+"}\n", 0o640)
	code, _, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	if _, err := os.Stat(env); err != nil {
		t.Fatal("an env file outside secrets was removed")
	}
}

func TestADropInThatCannotBeRemovedIsAWriteError(t *testing.T) {
	h := newSetupHost(t)
	h.add()
	h.fsys.failOn, h.fsys.failPath = "remove", "sard-repo-extra.conf"
	code, _, stderr := h.sudo("repo", "remove", "extra", "--config", "C")
	assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
}

func TestAPasswordFileAnotherKeyRefersToIsNotTakenOver(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories[0].PasswordFile = h.path("secrets/restic-extra.pass")
	h.saveConfig()
	h.write(h.path("secrets/restic-extra.pass"), "the password of base\n", 0o600)
	before := h.hostTree()
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitUsage, "PATH_IN_USE")
	if !strings.Contains(stderr, "password_file") || !strings.Contains(stderr, "base") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}

func TestAPathWithAControlCharacterIsRefusedAndLeavesNoDropIn(t *testing.T) {
	for _, path := range []string{"/srv/a\nExecStartPre=+/bin/true", "/srv/a\tb", "/srv/a\x7fb", "/srv/a\x00b"} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.sudo("repo", "add", "extra", path, "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "LOCAL_PATH_INVALID")
		h.assertAbsent(h.dropIns())
		h.assertHostUnchanged(before)
	}
}

func TestAnAddressInTheSummaryNeverShowsACredential(t *testing.T) {
	h := newSetupHost(t)
	c := newHostCmd("repo add", hostOptions{configPath: h.cfgPath}, hostsetup.Principal{}, &bytes.Buffer{}, &bytes.Buffer{}, h.deps)
	var out bytes.Buffer
	c.stdout = &out
	st := &addState{name: "extra", url: "rest:https://u:" + urlMarker + "@rest.example.com/extra", State: repoconnect.State{ID: "X"}}
	c.printAdded(st)
	c.printUnchanged(st, "X")
	if strings.Contains(out.String(), urlMarker) || !strings.Contains(out.String(), "u:***@rest.example.com") {
		t.Fatalf("output %q", out.String())
	}
}

func TestAnAddressThatIsASymbolicLinkIsRefusedAndNothingIsChanged(t *testing.T) {
	h := newSetupHost(t)
	target := h.path("elsewhere")
	ok(t, os.Mkdir(target, 0o755))
	ok(t, os.Symlink(target, h.extraDir()))
	before := h.hostTree()
	code, _, stderr := h.add()
	assertRefusal(t, code, stderr, exitUsage, "LOCAL_PATH_INVALID")
	if !strings.Contains(stderr, "symbolic link") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.dropIns())
	h.assertHostUnchanged(before)
	if _, ok := h.fsys.ownerOf(target); ok {
		t.Fatal("the directory behind the link was given away")
	}
}

func TestAnAddressThatPassesThroughASymbolicLinkIsRefusedAndNothingIsChanged(t *testing.T) {
	h := newSetupHost(t)
	real := h.path("var")
	ok(t, os.MkdirAll(filepath.Join(real, "backups"), 0o755))
	ok(t, os.MkdirAll(h.path("home"), 0o755))
	link := h.path("home/repos")
	ok(t, os.Symlink(real, link))
	before := h.hostTree()
	for _, address := range []string{filepath.Join(link, "backups"), filepath.Join(link, "new")} {
		code, _, stderr := h.sudo("repo", "add", "extra", address, "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "LOCAL_PATH_INVALID")
		if !strings.Contains(stderr, link+" is a symbolic link; give the resolved path") {
			t.Fatalf("stderr %q", stderr)
		}
	}
	h.assertNoBackendCalls()
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.dropIns(), filepath.Join(real, "new"))
	h.assertHostUnchanged(before)
	if _, ok := h.fsys.ownerOf(filepath.Join(real, "backups")); ok {
		t.Fatal("the directory behind the link was given away")
	}
}
