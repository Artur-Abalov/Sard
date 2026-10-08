// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// answers makes the fake restic answer the commands named by sub with the
// stderr line and the exit code.
func (r *fakeRepo) answers(sub, line string, code int) {
	r.script = func(s string, _ []string) (string, int, bool) { return line, code, s == sub }
}

func (h *setupHost) assertNoS3Traces() {
	h.t.Helper()
	h.assertAbsent(h.envPath(), h.path("secrets/restic-extra.pass"), h.path("agent.d/repo-extra.yaml"))
	if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
		h.t.Fatalf("temporary files in secrets: %v", left)
	}
	if h.sd.touched() {
		h.t.Fatalf("systemctl was called: %v", h.sd.calls)
	}
	if len(h.log.lines) != 0 {
		h.t.Fatalf("audit lines: %q", h.log.lines)
	}
}

// tempFilesIn are the names of the temporary files of the commands in dir.
func (h *setupHost) tempFilesIn(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			left = append(left, filepath.Join(dir, e.Name()))
		}
	}
	return left
}

// Rule "Секретный ключ S3 ... хранится только в env-файле пользователя службы".

func TestARejectedS3KeyLeavesNoEnvFileAndNoTemporaryFiles(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().answers("cat", "Fatal: unable to open config file: Stat: Access Denied.", 1)
	code, stdout, stderr := h.s3Cmd()
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	h.assertNoS3Traces()
	h.assertS3ValuesHidden(stdout, stderr)
}

func TestAnEnvFileAnotherKeyOfTheConfigNamesIsNotOverwritten(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories[0].EnvFile = h.envPath()
	h.saveConfig()
	before := h.hostTree()
	code, _, stderr := h.s3Cmd()
	assertRefusal(t, code, stderr, exitUsage, "PATH_IN_USE")
	if !strings.Contains(stderr, `env_file of repository "base"`) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}

func TestALeftoverEnvFileNobodyUsesIsReplacedByTheAcceptedKeys(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.envPath(), "AWS_ACCESS_KEY_ID=OLD\nAWS_SECRET_ACCESS_KEY=OLD-SECRET\n", 0o600)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.envPath()); !strings.Contains(got, "AWS_SECRET_ACCESS_KEY=S3-MARKER\n") || strings.Contains(got, "OLD") {
		t.Fatalf("env file %q", got)
	}
}

func TestAnEnvFileThatCannotBeReadAsASecretIsNotReadNorChanged(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil
	h.deps.readOwned = func(p string, _ uint32) ([]byte, error) {
		if p == h.envPath() {
			return nil, &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
		}
		return os.ReadFile(p)
	}
	before := h.hostTree()
	code, _, stderr := h.s3Cmd()
	assertRefusal(t, code, stderr, exitUsage, "SECRET_FILE_REJECTED")
	if !strings.Contains(stderr, h.envPath()) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestAnEnvFileIsReadOnlyThroughTheOwnerCheckedReadAsTheServiceUser(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.readOwned, h.plainReads = nil, nil
	code, _, _ = h.s3Cmd()
	assertCode(t, code, exitOK)
	var read bool
	for _, r := range h.readOwned {
		if r.path == h.envPath() {
			read = true
			if r.uid != serviceUID {
				t.Errorf("read for uid %d", r.uid)
			}
		}
	}
	if !read {
		t.Fatal("the env file was not read through the owner-checked read")
	}
	for _, p := range h.plainReads {
		if p == h.envPath() {
			t.Fatal("the env file was read by its bare path")
		}
	}
}

func TestAFailedWriteOfTheEnvFileIsAWriteErrorWithoutTraces(t *testing.T) {
	for _, op := range []string{"chown", "rename", "sync"} {
		h := newSetupHost(t)
		h.fsys.failOn, h.fsys.failPath = op, "restic-extra.env"
		code, _, stderr := h.s3Cmd()
		assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
		if !strings.Contains(stderr, "restic-extra.env") {
			t.Errorf("%s: stderr %q", op, stderr)
		}
		h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.envPath())
		if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
			t.Errorf("%s: temporary files %v", op, left)
		}
		if h.sd.touched() {
			t.Errorf("%s: systemctl was called", op)
		}
	}
}

// Rule "repo add подключает хранилище S3 одной командой".

func TestAnEmptyS3StorageIsCreatedWithAGeneratedPassword(t *testing.T) {
	h := newSetupHost(t)
	mainBefore := h.fileContent(h.cfgPath)
	code, _, stderr := h.s3Cmd()
	assertCode(t, code, exitOK)
	if init := h.restic.callsTo(s3Address, "init"); len(init) != 1 || init[0].passwordFile != h.path("secrets/restic-extra.pass") || envValue(init[0].env, "AWS_ACCESS_KEY_ID") != keyID1 {
		t.Fatalf("restic init: %+v", init)
	}
	pass := h.path("secrets/restic-extra.pass")
	h.assertOwner(pass, serviceUID)
	h.assertMode(pass, 0o600)
	if !base64URL43.MatchString(h.fileContent(pass)) {
		t.Fatalf("password file %q", h.fileContent(pass))
	}
	if h.fileContent(h.cfgPath) != mainBefore {
		t.Fatal("the main config changed")
	}
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestTheS3SummaryNamesTheRepositoryAndAsksForACopyOfThePassword(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.s3Cmd()
	assertCode(t, code, exitOK)
	for _, want := range []string{"extra", "s3", s3Address, h.envPath(), "created a new repository", "only on this host", "unrecoverable", "sudo sard-agent repo password extra --reveal"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if id := hexID.FindString(stdout); id != h.s3Repo().id {
		t.Errorf("repository_id %q in %s", id, stdout)
	}
	if strings.Contains(stdout, "lost together with this host") {
		t.Errorf("stdout warns about a backup on this host:\n%s", stdout)
	}
	h.assertS3ValuesHidden(stdout, stderr)
}

func TestAnS3RepositoryNeedsNoSystemdDropIn(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.assertAbsent(h.dropIns())
	for _, c := range h.sd.calls {
		if strings.Contains(c, "daemon-reload") {
			t.Fatalf("systemctl: %v", h.sd.calls)
		}
	}
}

func TestAnExistingS3RepositoryIsAttachedWithItsPassword(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	src := h.path("outside/F")
	h.write(src, passMarker+"\n", 0o600)
	code, stdout, _ := h.s3Cmd("--password-from-file", src)
	assertCode(t, code, exitOK)
	if n := len(h.restic.callsTo(s3Address, "init")); n != 0 {
		t.Fatalf("restic init was called %d times", n)
	}
	if !strings.Contains(stdout, "attached an existing repository") || !strings.Contains(stdout, h.s3Repo().id) {
		t.Fatalf("stdout %q", stdout)
	}
	pass := h.path("secrets/restic-extra.pass")
	if h.fileContent(pass) != passMarker+"\n" {
		t.Fatalf("password file %q", h.fileContent(pass))
	}
	h.assertOwner(pass, serviceUID)
	h.assertMode(pass, 0o600)
}

func TestThePasswordOfAnExistingS3RepositoryIsAskedAfterTheSecretKey(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	term := h.terminalIs(s3Marker, s3Marker, passMarker, passMarker)
	code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertCode(t, code, exitOK)
	if len(term.prompts) != 4 || !strings.Contains(term.prompts[0], "secret key") || !strings.Contains(term.prompts[2], "password") {
		t.Fatalf("prompts %q", term.prompts)
	}
}

func TestAnExistingS3RepositoryWithoutAPasswordSourceIsRefusedWithoutTraces(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	h.hung = &hungInput{}
	code, _, stderr := h.s3Cmd()
	assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_MISSING")
	for _, flag := range []string{"--password-stdin", "--password-from-file"} {
		if !strings.Contains(stderr, flag) {
			t.Errorf("stderr does not name %s: %q", flag, stderr)
		}
	}
	h.assertNoS3Traces()
}

func TestAWrongPasswordOfAnExistingS3RepositoryIsRefusedWithoutTraces(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, "another"
	src := h.path("outside/F")
	h.write(src, passMarker+"\n", 0o600)
	code, stdout, stderr := h.s3Cmd("--password-from-file", src)
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	h.assertNoS3Traces()
	h.assertS3ValuesHidden(stdout, stderr)
}

func TestAResticRefusalWhileCreatingAnS3RepositoryKeepsTheFilesForARepeat(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().answers("init", "Fatal: create key in repository at "+s3Address+" failed: client.PutObject: Internal Error", 1)
	code, _, stderr := h.s3Cmd()
	assertRefusal(t, code, stderr, exitAgentError, "BACKEND_REFUSED")
	if !strings.Contains(stderr, "Internal Error") || !strings.Contains(stderr, "will be used when the command is repeated") {
		t.Fatalf("stderr %q", stderr)
	}
	for _, p := range []string{h.envPath(), h.path("secrets/restic-extra.pass")} {
		h.assertOwner(p, serviceUID)
		h.assertMode(p, 0o600)
		if !strings.Contains(stderr, p) {
			t.Errorf("stderr does not name %s", p)
		}
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
	if h.sd.touched() {
		t.Fatalf("systemctl: %v", h.sd.calls)
	}
}

func TestRepeatingAfterAFailureOfS3UsesTheLeftoverFilesWithoutQuestions(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.envPath(), "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\n", 0o600)
	h.write(h.path("secrets/restic-extra.pass"), "Q\n", 0o600)
	term := h.terminalIs()
	code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertCode(t, code, exitOK)
	if len(term.prompts) != 0 {
		t.Fatalf("the terminal was asked: %q", term.prompts)
	}
	init := h.restic.callsTo(s3Address, "init")
	if len(init) != 1 || init[0].password != "Q" || envValue(init[0].env, "AWS_SECRET_ACCESS_KEY") != s3Marker {
		t.Fatalf("restic init: %+v", init)
	}
}

func TestRepeatingAnS3CommandChangesNothingAndAsksNothing(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	id := h.s3Repo().id
	h.sd.calls, h.log.lines = nil, nil
	term := h.terminalIs()
	before := h.hostTree()
	inits := len(h.restic.callsTo(s3Address, "init"))
	code, stdout, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") || !strings.Contains(stdout, id) {
		t.Fatalf("stdout %q", stdout)
	}
	if len(term.prompts) != 0 || len(h.restic.callsTo(s3Address, "init")) != inits {
		t.Fatalf("prompts %q", term.prompts)
	}
	h.assertHostUnchanged(before)
}

func TestRepeatingAnS3CommandWithTheSameSecretKeyChangesNothing(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil
	before := h.hostTree()
	code, stdout, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertHostUnchanged(before)
}

func TestAnS3NameAtAnotherAddressIsAConflict(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil
	before := h.hostTree()
	code, _, stderr := h.s3At("s3:https://s3.example.com/bucket-b/other", s3Marker)
	assertRefusal(t, code, stderr, exitIdentityExists, "REPOSITORY_CONFLICT")
	if !strings.Contains(stderr, s3Address) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertHostUnchanged(before)
}

func TestAfterConnectingS3TheChangeIsAppliedAndAudited(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra added") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

// Н17: new keys of a connected repository.

func (h *setupHost) connectedS3() {
	h.t.Helper()
	code, _, stderr := h.s3Cmd()
	assertCode(h.t, code, exitOK)
	_ = stderr
	h.sd.calls, h.log.lines = nil, nil
}

func TestNewKeysOfAConnectedS3RepositoryReplaceTheEnvFileAfterTheCheck(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	fragment := h.fileContent(h.path("agent.d/repo-extra.yaml"))
	pass := h.fileContent(h.path("secrets/restic-extra.pass"))
	h.stdinIs(s3Marker + "-2")
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-2", "--secret-key-stdin", "--config", "C")
	assertCode(t, code, exitOK)
	want := "AWS_ACCESS_KEY_ID=KEY-ID-2\nAWS_SECRET_ACCESS_KEY=S3-MARKER-2\n"
	if got := h.fileContent(h.envPath()); got != want {
		t.Fatalf("env file %q", got)
	}
	h.assertOwner(h.envPath(), serviceUID)
	h.assertMode(h.envPath(), 0o600)
	if !strings.Contains(stdout, "credentials updated") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertRotationLeftOnlyTheEnvFile(fragment, pass)
	h.assertValuesHidden(stdout, stderr)
}

// assertRotationLeftOnlyTheEnvFile: audited, no restart, nothing else written.
func (h *setupHost) assertRotationLeftOnlyTheEnvFile(fragment, pass string) {
	h.t.Helper()
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra credentials updated") {
		h.t.Fatalf("audit %q", h.log.lines)
	}
	if h.sd.touched() {
		h.t.Fatalf("systemctl: %v", h.sd.calls)
	}
	if h.fileContent(h.path("agent.d/repo-extra.yaml")) != fragment || h.fileContent(h.path("secrets/restic-extra.pass")) != pass {
		h.t.Fatal("the fragment or the password file changed")
	}
	if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
		h.t.Fatalf("temporary files %v", left)
	}
	if n := len(h.restic.callsTo(s3Address, "init")); n != 1 {
		h.t.Fatalf("restic init was called %d times", n)
	}
}

func TestARejectedNewKeyLeavesTheEnvFileOfAConnectedS3RepositoryAlone(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	h.s3Repo().script = func(sub string, env []string) (string, int, bool) {
		return "Fatal: unable to open config file: Stat: Access Denied.", 1, sub == "cat" && envValue(env, "AWS_ACCESS_KEY_ID") == "KEY-ID-2"
	}
	before := h.hostTree()
	h.stdinIs(s3Marker + "-2")
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-2", "--secret-key-stdin", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	h.assertHostUnchanged(before)
	if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
		t.Fatalf("temporary files %v", left)
	}
}

func TestANewRegionOfAConnectedS3RepositoryIsAChangeOfKeysToo(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	code, stdout, _ := h.s3Cmd("--region", "ru-central1")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "credentials updated") || !strings.Contains(h.fileContent(h.envPath()), "AWS_DEFAULT_REGION=ru-central1\n") {
		t.Fatalf("stdout %q env %q", stdout, h.fileContent(h.envPath()))
	}
}

func TestANewKeyIDWithoutASecretKeySourceAsksTheTerminal(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	term := h.terminalIs("S3-MARKER-3", "S3-MARKER-3")
	code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", "KEY-ID-3", "--config", "C")
	assertCode(t, code, exitOK)
	if len(term.prompts) != 2 || !strings.Contains(h.fileContent(h.envPath()), "KEY-ID-3") {
		t.Fatalf("prompts %q env %q", term.prompts, h.fileContent(h.envPath()))
	}
}

// Rule "Удалённые репозитории показываются и удаляются без раскрытия ключей".

func TestRemovingAnS3RepositoryRemovesTheFragmentAndTheEnvFileButKeepsThePassword(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	calls := len(h.restic.calls)
	code, _, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.envPath())
	if _, err := os.Stat(h.path("secrets/restic-extra.pass")); err != nil {
		t.Fatalf("the password file: %v", err)
	}
	if len(h.restic.calls) != calls {
		t.Fatal("restic was called")
	}
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra removed") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestTheCardOfAnS3RepositoryNamesTheEnvFileAndHidesTheKeys(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	code, stdout, stderr := h.sudo("repo", "show", "extra", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	for _, want := range []string{`"backend":"s3"`, `"address":"` + s3Address + `"`, `"env_file":"` + h.envPath() + `"`, `"defined_in":"` + h.path("agent.d/repo-extra.yaml") + `"`} {
		if !strings.Contains(strings.ReplaceAll(stdout, ": ", ":"), want) {
			t.Errorf("stdout lacks %s:\n%s", want, stdout)
		}
	}
	h.assertS3ValuesHidden(stdout, stderr)
}

// A repeat that cannot reach the storage with the keys in place fails, it
// does not report success.
func TestARepeatThatTheStorageRefusesIsNotASuccess(t *testing.T) {
	h := newSetupHost(t)
	h.connectedS3()
	h.s3Repo().answers("cat", "Fatal: unable to open config file: Stat: Access Denied.", 1)
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
}
