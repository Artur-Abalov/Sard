// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "repo add подключает хранилище SFTP одной командой".

func TestAnEmptySFTPDirectoryBecomesARepositoryWithAGeneratedPassword(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, stdout, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	inits := h.restic.callsTo(sftpAddress, "init")
	if len(inits) != 1 || inits[0].runAs == nil || inits[0].runAs.UID != serviceUID || inits[0].runAs.GID != serviceUID {
		t.Fatalf("restic init: %+v", inits)
	}
	h.assertSFTPFragment()
	if entries, _ := os.ReadDir(h.dropIns()); len(entries) != 0 {
		t.Errorf("a drop-in was written: %v", entries)
	}
	h.assertSFTPSummary(stdout)
	h.assertSFTPValuesHidden(stdout, stderr)
}

// assertSFTPFragment: name, url and password file, and no env file.
func (h *sftpHostWorld) assertSFTPFragment() {
	h.t.Helper()
	fragment := h.fileContent(h.path("agent.d/repo-extra.yaml"))
	for _, want := range []string{"name: extra", "url: " + sftpAddress, "password_file: " + h.path("secrets/restic-extra.pass")} {
		if !strings.Contains(fragment, want) {
			h.t.Errorf("fragment lacks %q:\n%s", want, fragment)
		}
	}
	if strings.Contains(fragment, "env_file") {
		h.t.Errorf("an sftp fragment has no env file:\n%s", fragment)
	}
}

// assertSFTPSummary: the repository, the keys and the warning about the password.
func (h *sftpHostWorld) assertSFTPSummary(stdout string) {
	h.t.Helper()
	for _, want := range []string{"extra", "sftp", sftpAddress, hexID.FindString(stdout), "ssh-ed25519 AAAAexistingkey", keyED.fingerprint(), "only on this host", "unrecoverable"} {
		if want == "" || !strings.Contains(stdout, want) {
			h.t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "lost together with this host") {
		h.t.Errorf("stdout warns of a backup on the same host:\n%s", stdout)
	}
}

func TestAnExistingSFTPRepositoryIsAttachedByItsPassword(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	repo := h.sftpRepo()
	repo.initialized, repo.password = true, passMarker
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.sftpCmd("--password-stdin")
	assertCode(t, code, exitOK)
	if len(h.restic.callsTo(sftpAddress, "init")) != 0 {
		t.Fatal("restic init ran")
	}
	if !strings.Contains(stdout, "attached an existing repository") || !strings.Contains(stdout, repo.id) {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertSFTPValuesHidden(stdout, stderr)
}

func TestTheFirstRunWithAnUnauthorizedKeyPrintsThePublicPartAndRefuses(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.authorized = false
	code, stdout, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "SSH_KEY_NOT_AUTHORIZED")
	for _, want := range []string{"ssh-ed25519 AAAAnewkey sard-agent@host1", "authorized_keys", "backup", sftpHost, "repeat"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	h.assertNoSFTPTraces()
	if h.restic.backendCalls() != 0 {
		t.Fatalf("restic was asked: %v", h.restic.subs())
	}
	h.assertSFTPValuesHidden(stdout, stderr)
}

func TestTheFilesWrittenBeforeARefusedLoginStayAndAreAudited(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.authorized = false
	h.sftpCmd()
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "config"} {
		if _, err := os.Stat(h.sshFile(name)); err != nil {
			t.Errorf("%s is gone: %v", name, err)
		}
	}
	got := strings.Join(h.log.lines, "\n")
	if len(h.log.lines) != 2 || !strings.Contains(got, "ssh key of service user sard-agent created") || !strings.Contains(got, "ssh host key of nas.example.com trusted") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestAfterTheKeyIsAuthorizedARepeatConnectsWithoutQuestionsAndWithoutANewKey(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.authorized = false
	h.sftpCmd()
	h.srv.authorized = true
	h.log.lines = nil
	term := h.terminalIs()
	keygens := len(h.ssh.called("ssh-keygen"))
	code, _, stderr := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	_ = stderr
	if len(term.prompts) != 0 || len(h.ssh.called("ssh-keygen")) != keygens {
		t.Fatalf("prompts %q, keygens %d", term.prompts, len(h.ssh.called("ssh-keygen")))
	}
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository extra added") {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestRepeatingAnSFTPCommandChangesNothingAndAsksNothing(t *testing.T) {
	h := newSFTPHost(t)
	stdout := h.connectedSFTP()
	id := hexID.FindString(stdout)
	term := h.terminalIs()
	before := h.hostTree()
	code, stdout, _ := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") || !strings.Contains(stdout, id) || len(term.prompts) != 0 {
		t.Fatalf("stdout %q, prompts %q", stdout, term.prompts)
	}
	h.assertHostUnchanged(before)
}

func TestAfterConnectingSFTPTheChangeIsApplied(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, stdout, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	h.assertApplied(stdout)
}

func TestWhileAnotherCommandChangesTheConfigNoClientProgramRuns(t *testing.T) {
	h := newSFTPHost(t)
	unlock := h.holdConfigLock()
	defer unlock()
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitTemporary, "CONFIG_LOCKED")
	h.assertNoSSHProgram()
	h.assertSSHUnchanged(before)
}

func TestUnderSudoEveryProgramRunsAsTheServiceUser(t *testing.T) {
	h := newSFTPHost(t)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	if len(h.ssh.calls) < 3 {
		t.Fatalf("calls %+v", h.ssh.calls)
	}
	for _, c := range h.ssh.calls {
		assertRanAsService(t, c.program, c.runAs)
	}
	for _, c := range h.restic.calls {
		assertRanAsService(t, "restic "+c.sub, c.runAs)
	}
}

func assertRanAsService(t *testing.T, what string, as *restic.RunAs) {
	t.Helper()
	if as == nil || as.UID != serviceUID || as.GID != serviceUID {
		t.Errorf("%s ran as %+v", what, as)
	}
}

func TestThePasswordAndTheKeysNeverReachTheArgumentsOrTheEnvironmentOfTheClient(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	repo := h.sftpRepo()
	repo.initialized, repo.password = true, passMarker
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.sftpCmd("--password-stdin")
	assertCode(t, code, exitOK)
	for _, c := range h.ssh.calls {
		assertNoSecretIn(t, c)
	}
	h.assertSFTPValuesHidden(stdout, stderr)
}

func assertNoSecretIn(t *testing.T, c sshCall) {
	t.Helper()
	for _, a := range c.args {
		if strings.Contains(a, passMarker) || strings.Contains(a, "PRIVATE-KEY-MARKER") {
			t.Errorf("%s got a secret in an argument %q", c.program, a)
		}
	}
	for _, e := range c.env {
		if strings.Contains(e, passMarker) || strings.HasPrefix(e, "AWS_") || strings.HasPrefix(e, "RESTIC_") {
			t.Errorf("%s got %q in its environment", c.program, e)
		}
	}
}

func TestTheClientRunsWithAFixedEnvironmentOfThePathOfResticAndTheHome(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	for _, c := range h.ssh.calls {
		got := strings.Join(c.env, " ")
		want := "PATH=" + h.programsDir() + " HOME=" + h.homeDir() + " LC_ALL=C"
		if got != want {
			t.Errorf("%s: env %q, want %q", c.program, got, want)
		}
	}
}

// Rule "Проверка доступа ограничена таймаутом подключения" (SFTP).

func TestAServerThatDoesNotAnswerTheScanIsTemporaryAfterTheConnectTimeoutAndNothingIsWritten(t *testing.T) {
	h := newSFTPHost(t)
	vc := newVirtualClock()
	h.deps.clock = vc
	h.srv.hang = map[string]bool{"ssh-keyscan": true}
	before := h.sshTree()
	done := h.runInBackground("repo", "add", "extra", sftpAddress, "--host-key-fingerprint", keyED.fingerprint(), "--connect-timeout", "30s", "--config", "C")
	<-h.ssh.hungCh
	vc.waitTimers(t, 2)
	vc.advance(29 * time.Second)
	stillRunning(t, done)
	vc.advance(time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	for _, want := range []string{sftpHost, "22"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	if len(h.srv.terminated) != 1 || h.srv.terminated[0] != "ssh-keyscan" {
		t.Errorf("terminated %v", h.srv.terminated)
	}
	h.assertSSHUnchanged(before)
	h.assertNoSFTPTraces()
}

func TestAServerThatDoesNotAnswerTheLoginIsTemporaryAfterTheConnectTimeout(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	vc := newVirtualClock()
	h.deps.clock = vc
	h.srv.hang = map[string]bool{"sftp": true}
	done := h.runInBackground("repo", "add", "extra", sftpAddress, "--connect-timeout", "30s", "--config", "C")
	<-h.ssh.hungCh
	vc.waitTimers(t, 3)
	vc.advance(30 * time.Second)
	r := within(t, done)
	assertRefusal(t, r.code, r.stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	h.assertNoSFTPTraces()
}

// Rule "Отказ хранилища называет свой класс и причину" (SFTP).

func TestTheLoginCheckTellsTheClassEvenWhenResticPassesOnNoReasonOfSSH(t *testing.T) {
	for _, c := range []struct {
		stderr string
		code   int
		reason string
		text   string
	}{
		{"backup@nas.example.com: Permission denied (publickey).", exitUsage, "SSH_KEY_NOT_AUTHORIZED", "Permission denied (publickey)"},
		{"Host key verification failed.", exitTrust, "HOST_KEY_MISMATCH", "Host key verification failed"},
		{"ssh: Could not resolve hostname nas.example.com: Name or service not known", exitTemporary, "BACKEND_UNAVAILABLE", "Could not resolve hostname"},
		{"ssh: connect to host nas.example.com port 22: Connection refused", exitTemporary, "BACKEND_UNAVAILABLE", "Connection refused"},
		{"ssh: connect to host nas.example.com port 22: Connection timed out", exitTemporary, "BACKEND_UNAVAILABLE", "Connection timed out"},
	} {
		h := newSFTPHost(t)
		h.keyAndHostKnown()
		h.srv.loginStderr, h.srv.loginCode = c.stderr, 255
		h.sftpRepo().answers("cat", "Fatal: unable to open repository at "+sftpAddress+": unable to start the sftp session, error: EOF", 1)
		code, stdout, stderr := h.sftpAt(sftpAddress)
		assertRefusal(t, code, stderr, c.code, c.reason)
		if !strings.Contains(stderr, c.text) {
			t.Errorf("%s: stderr lacks %q:\n%s", c.reason, c.text, stderr)
		}
		h.assertNoSFTPTraces()
		h.assertSFTPValuesHidden(stdout, stderr)
	}
}

func TestADirectoryOfTheSFTPServerWithoutTheRightToWriteIsAccessDenied(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.sftpRepo().answers("cat", "unable to create lock in backend: OpenFile /srv/extra/locks/1a2b: permission denied", 1)
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	for _, want := range []string{"permission denied", "/srv/extra"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	h.assertNoSFTPTraces()
}

func TestADirectoryOfTheSFTPServerThatCannotBeCreatedKeepsThePasswordForARepeat(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.sftpRepo().answers("init", "Fatal: create repository at "+sftpAddress+" failed: sftp: MkdirAll /srv/extra/keys: permission denied", 1)
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	for _, want := range []string{"permission denied", "/srv/extra", "will be used when the command is repeated", h.path("secrets/restic-extra.pass")} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	h.assertOwner(h.path("secrets/restic-extra.pass"), serviceUID)
	h.assertMode(h.path("secrets/restic-extra.pass"), 0o600)
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
	if h.sd.touched() {
		t.Fatalf("systemctl: %v", h.sd.calls)
	}
}

// Rule "Удалённые репозитории показываются и удаляются без раскрытия ключей" (SFTP).

func TestRemovingAnSFTPRepositoryLeavesTheSSHFilesAndNamesThem(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	before := h.sshTree()
	code, stdout, _ := h.sudo("repo", "remove", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	delete(before, h.path("agent.d/repo-extra.yaml"))
	for path := range before {
		if strings.HasPrefix(path, h.homeDir()) {
			continue
		}
		delete(before, path)
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
	for _, want := range []string{h.sshFile("id_ed25519"), sftpHost, "remain"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, name := range []string{"id_ed25519", "known_hosts", "config"} {
		if _, err := os.Stat(h.sshFile(name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	h.assertSSHUnchanged(before)
}

func TestTheAuditLinesOfARemoteConnectionHoldNoValues(t *testing.T) {
	for name, c := range map[string]struct {
		prepare func(h *sftpHostWorld)
		lines   []string
	}{
		"a key and a host key exist": {func(h *sftpHostWorld) {
			h.putSSH("id_ed25519", "PRIVATE-KEY-MARKER", 0o600)
			h.putSSH("id_ed25519.pub", "ssh-ed25519 AAAAk x\n", 0o644)
		},
			[]string{"ssh host key of nas.example.com trusted", "repository extra added"}},
		"nothing exists": {func(h *sftpHostWorld) {},
			[]string{"ssh key of service user sard-agent created", "ssh host key of nas.example.com trusted", "repository extra added"}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newSFTPHost(t)
			c.prepare(h)
			code, stdout, stderr := h.sftpCmd()
			assertCode(t, code, exitOK)
			if len(h.log.lines) != len(c.lines) {
				t.Fatalf("audit %q", h.log.lines)
			}
			for i, want := range c.lines {
				if !strings.Contains(h.log.lines[i], want) || !strings.Contains(h.log.lines[i], "alice") || !strings.Contains(h.log.lines[i], "1000") {
					t.Errorf("audit line %d %q lacks %q, alice or 1000", i, h.log.lines[i], want)
				}
			}
			h.assertSFTPValuesHidden(stdout, stderr)
		})
	}
}

func TestAChangeOfOnlyTheSSHFilesDoesNotRestartTheService(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	ok(t, os.Remove(h.sshFile("config")))
	h.log.lines = nil
	code, stdout, _ := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	if h.sd.restarted() || h.sd.touched() {
		t.Fatalf("systemctl: %v", h.sd.calls)
	}
	if !strings.Contains(stdout, "unchanged") {
		t.Fatalf("stdout %q", stdout)
	}
	if got := strings.Join(h.log.lines, "\n"); strings.Contains(got, "repository extra added") {
		t.Fatalf("audit %q", h.log.lines)
	}
	if got := h.fileContent(h.sshFile("config")); got != hostBlock {
		t.Fatalf("config %q", got)
	}
}

// Rule "Справка repo add описывает удалённые хранилища".

func TestTheHelpOfRepoAddNamesTheSFTPFlagsAndWhereTheSSHFilesAre(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "add", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"--host-key-fingerprint", "--replace-host-key", "--connect-timeout", "--access-key-id", "--region",
		"--secret-key-stdin", "--secret-key-from-file", "yes", "known_hosts", "passwd", "SSH_CLIENT_MISSING", "HOST_KEY_UNCONFIRMED",
		"HOST_KEY_CHANGED", "SSH_KEY_NOT_AUTHORIZED", "SSH_FILE_REJECTED"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if strings.Contains(stdout, "now sftp:") {
		t.Errorf("help still says sftp is not supported:\n%s", stdout)
	}
}

// Р43: the ssh files stay when restic refuses, the password file and the
// fragment do not exist yet.
func TestTheSSHFilesStayWhenResticRefusesTheStorage(t *testing.T) {
	h := newSFTPHost(t)
	h.sftpRepo().answers("cat", "unable to create lock in backend: OpenFile /srv/extra/locks/1a2b: permission denied", 1)
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "config"} {
		if _, err := os.Stat(h.sshFile(name)); err != nil {
			t.Errorf("%s is gone: %v", name, err)
		}
	}
	h.assertNoSFTPTraces()
	if !strings.Contains(stderr, "stay") {
		t.Errorf("stderr does not say the ssh files stay:\n%s", stderr)
	}
}

// T1: a repeat that sets the ssh files up again never touches the password
// file of the repository that is connected already.
func TestAnSSHFilesUpdateNeverReplacesThePasswordFile(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	pass := h.path("secrets/restic-extra.pass")
	before := h.fileContent(pass)
	ok(t, os.Remove(h.sshFile("config")))
	other := h.path("outside/other.pass")
	h.write(other, "another-valid-key\n", 0o600)
	h.stdinIs("unread")
	code, _, stderr := h.sftpAt(sftpAddress, "--password-from-file", other)
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--password-from-file") || h.fileContent(pass) != before {
		t.Fatalf("stderr %q, password file %q", stderr, h.fileContent(pass))
	}
	h.assertAbsent(h.sshFile("config"))

	code, _, stderr = h.sftpAt(sftpAddress, "--password-stdin")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--password-stdin") || h.stdin.String() != "unread" || h.fileContent(pass) != before {
		t.Fatalf("stderr %q, stdin %q", stderr, h.stdin.String())
	}
}

func TestAnSSHFilesUpdateWithAWrongPasswordFileIsWrongPasswordWithoutAQuestion(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	pass := h.path("secrets/restic-extra.pass")
	h.write(pass, "not-the-password\n", 0o600)
	ok(t, os.Remove(h.sshFile("config")))
	term := h.terminalIs("whatever", "whatever")
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	if len(term.prompts) != 0 || h.fileContent(pass) != "not-the-password\n" {
		t.Fatalf("prompts %q, password file %q", term.prompts, h.fileContent(pass))
	}
}

func TestAnSSHFilesUpdateOfARepositoryThatIsGoneIsAConflictAndNeverCreatesIt(t *testing.T) {
	h := newSFTPHost(t)
	h.connectedSFTP()
	pass := h.path("secrets/restic-extra.pass")
	before := h.fileContent(pass)
	h.sftpRepo().initialized = false
	ok(t, os.Remove(h.sshFile("config")))
	inits := len(h.restic.callsTo(sftpAddress, "init"))
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitIdentityExists, "REPOSITORY_CONFLICT")
	if len(h.restic.callsTo(sftpAddress, "init")) != inits || h.fileContent(pass) != before {
		t.Fatal("init ran or the password file changed")
	}
	if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 {
		t.Fatalf("temporary files %v", left)
	}
}
