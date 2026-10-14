// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"net"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// Rules "repo restore выполняется только через sudo", "... работает без
// сервера и без регистрации агента", "Хранилище читает пользователь службы,
// root не оставляет своих файлов в его каталогах", "Каждое восстановление
// оставляет строку аудита без значений", "Пароли и ключи не появляются...".

func TestOnlyRootMayRestore(t *testing.T) {
	for name, run := range map[string]func(h *setupHost, args ...string) (int, string, string){
		"service user": (*setupHost).asService, "other": (*setupHost).asOther,
	} {
		h := restoreHost(t)
		before := h.hostTree()
		code, _, stderr := run(h, "repo", "restore", "base", idF, "--target", h.target(), "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		if !strings.Contains(stderr, "sudo sard-agent repo restore") {
			t.Errorf("%s: stderr %q", name, stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
	}
}

func TestWithoutTheServiceUserTheRestoreDoesNotStart(t *testing.T) {
	h := restoreHost(t)
	h.people = nil
	before := h.hostTree()
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitUsage, "SERVICE_USER_UNKNOWN")
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}

func TestTheTargetIsJudgedBeforeTheServiceUserIsLookedFor(t *testing.T) {
	h := restoreHost(t)
	h.people = nil
	code, _, stderr := h.sudo("repo", "restore", "base", idF, "--target", "relative", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "TARGET_INVALID")
}

func TestTheRestoreNeedsNoServerAndNoRegistration(t *testing.T) {
	h := restoreHost(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	ok(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	connections := make(chan struct{}, 10)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			connections <- struct{}{}
			_ = c.Close()
		}
	}()
	h.cfg.Server.Address = listener.Addr().String()
	h.saveConfig()
	h.sd.active = false
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	if len(connections) != 0 {
		t.Error("the server was contacted")
	}
	for _, p := range append(slices.Clone(h.plainReads), ownedPaths(h.readOwned)...) {
		if strings.Contains(p, "/tls/") {
			t.Errorf("%s was read", p)
		}
	}
}

func ownedPaths(reads []ownedRead) []string {
	var paths []string
	for _, r := range reads {
		paths = append(paths, r.path)
	}
	return paths
}

func TestTheRestoreDoesNotChangeTheSetupOfTheHostOrTouchTheService(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	changed := diff(before, h.hostTree())
	changed = slices.DeleteFunc(changed, func(p string) bool { return strings.HasPrefix(p, h.target()) })
	if len(changed) != 0 {
		t.Errorf("the host changed: %v", changed)
	}
	if h.sd.touched() {
		t.Errorf("systemctl was called: %v", h.sd.calls)
	}
	h.assertAbsent(h.agentD() + "/.sard-config.lock")
}

func TestResticLeavesNoFileInTheCacheOrTheRepository(t *testing.T) {
	h := restoreHost(t)
	before := h.hostTree()
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	for _, p := range diff(before, h.hostTree()) {
		if strings.HasPrefix(p, h.cacheDir()) || strings.HasPrefix(p, h.path("base")) {
			t.Errorf("%s changed", p)
		}
	}
	args := h.restoreCalls()[0].args
	if !slices.Contains(args, "--no-cache") || !slices.Contains(args, "--no-lock") {
		t.Errorf("restic args %q", args)
	}
}

func TestResticRestoresAsRootAndReadsAsTheServiceUser(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	if c := h.restoreCalls()[0]; c.runAs != nil {
		t.Errorf("restore ran as %+v: the files get the owners of the snapshot only from root", c.runAs)
	}
	for _, c := range h.restic.callsTo(h.path("base"), "snapshots") {
		if c.runAs == nil || c.runAs.UID != serviceUID {
			t.Errorf("the list ran as %+v", c.runAs)
		}
	}
	h2 := restoreHost(t)
	h2.restore("aaaa1111", "--path", "/srv/data/sub")
	for _, c := range h2.restic.callsTo(h2.path("base"), "ls") {
		if c.runAs == nil || c.runAs.UID != serviceUID {
			t.Errorf("ls ran as %+v", c.runAs)
		}
	}
}

// Р78: restic of the service user reads the password file by its path; restic
// as root gets the password from the pipe the agent filled, never a path of a
// file of the service user.
func TestResticOfTheServiceUserReadsThePasswordFileByItsPath(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	list := h.restic.callsTo(h.path("base"), "snapshots")[0]
	if list.runAs == nil || list.runAs.UID != serviceUID || list.passwordFile != h.path("secrets/base.pass") || list.password != passMarker {
		t.Errorf("the list: ran as %+v, env %q", list.runAs, list.env)
	}
}

func TestResticAsRootGetsThePasswordFromAnInheritedPipe(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	c := h.restoreCalls()[0]
	if c.passwordFile != "" || c.password != passMarker {
		t.Errorf("env %q, password %q", c.env, c.password)
	}
	i := slices.Index(c.args, "--password-file")
	if i < 0 || !strings.HasPrefix(c.args[i+1], "/proc/self/fd/") {
		t.Errorf("args %q: the password must come from an inherited descriptor", c.args)
	}
}

func TestNoPasswordAndNoPathOfItsFileReachResticAsRoot(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	c := h.restoreCalls()[0]
	for _, kv := range append(slices.Clone(c.env), c.args...) {
		if strings.Contains(kv, passMarker) || strings.Contains(kv, h.path("secrets")) {
			t.Errorf("the password or its file in %q", kv)
		}
	}
}

// restoreSFTPHost has the repository extra at an sftp: address, with the snapshots of base.
func restoreSFTPHost(t *testing.T) (*setupHost, string) {
	t.Helper()
	h := restoreHost(t)
	url := "sftp://backup@sftp.example.com:2222/srv/restic"
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAML("extra", url, h.path("secrets/base.pass"))), 0o640)
	repo := h.restic.repo(url)
	repo.initialized = true
	repo.snapshots = snapshotSet()
	repo.restoreState.files = h.base().restoreState.files
	return h, url
}

func TestSSHForAnSFTPRepositoryRunsAsTheServiceUser(t *testing.T) {
	h, url := restoreSFTPHost(t)
	code, _, stderr := h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Errorf("stderr %q", stderr)
	}
	calls := h.restic.callsTo(url, "restore")
	if len(calls) != 1 {
		t.Fatalf("restore calls %v", calls)
	}
	command := parseRestoreArgs(calls[0].args).sftpCommand
	want := "setpriv --reuid=990 --regid=990 --clear-groups ssh -p 2222 -l backup -s -- sftp.example.com sftp"
	if command != want {
		t.Errorf("sftp.command = %q, want %q", command, want)
	}
	for _, opened := range h.fsys.opened {
		if strings.Contains(opened, ".ssh") {
			t.Errorf("%s was opened by root", opened)
		}
	}
}

// Р77
func TestAnSFTPRepositoryWithoutSetprivIsRefusedBeforeResticIsCalled(t *testing.T) {
	h, _ := restoreSFTPHost(t)
	ok(t, os.Remove(h.path("bin/setpriv")))
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
	assertRefusal(t, code, stderr, exitAgentError, "SETPRIV_MISSING")
	for _, want := range []string{"setpriv", "util-linux"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}

func TestALocalRepositoryNeedsNoSetpriv(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.Remove(h.path("bin/setpriv")))
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
}

func TestOtherRepositoriesGetNoSFTPCommand(t *testing.T) {
	h := restoreHost(t)
	h.restore(idF)
	if command := parseRestoreArgs(h.restoreCalls()[0].args).sftpCommand; command != "" {
		t.Errorf("sftp.command = %q", command)
	}
}

func TestS3KeysReachResticByTheEnvironmentOnly(t *testing.T) {
	h := restoreHost(t)
	url := "s3:https://s3.example.com/bucket-b/extra"
	env := h.path("secrets/restic-extra.env")
	h.write(env, "AWS_ACCESS_KEY_ID=K\nAWS_SECRET_ACCESS_KEY="+s3Marker+"\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAMLWithEnv("extra", url, h.path("secrets/base.pass"), env)), 0o640)
	repo := h.restic.repo(url)
	repo.snapshots = snapshotSet()
	repo.restoreState.files = h.base().restoreState.files
	code, _, _ := h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
	assertCode(t, code, exitOK)
	c := h.restic.callsTo(url, "restore")[0]
	if strings.Contains(strings.Join(c.args, " "), s3Marker) {
		t.Errorf("a key in the arguments: %q", c.args)
	}
	if !slices.Contains(c.env, "AWS_SECRET_ACCESS_KEY="+s3Marker) {
		t.Errorf("the key is not in the environment: %q", c.env)
	}
	if !slices.Contains(h.readOwned, ownedRead{env, serviceUID}) {
		t.Errorf("the env file was not read as a file of the service user: %v", h.readOwned)
	}
}

func TestNoSecretReachesTheOutputOfTheRestore(t *testing.T) {
	for _, code := range []int{1, 0} {
		h, url := leakyS3Host(t)
		repo := h.restic.repo(url)
		repo.script = func(sub string, _ []string) (string, int, bool) {
			return "Fatal: " + s3Marker + " " + passMarker + " " + url, code, sub == "restore"
		}
		repo.snapshots = snapshotSet()
		repo.restoreState.files = h.base().restoreState.files
		got, stdout, stderr := h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
		if (got == exitOK) != (code == 0) {
			t.Fatalf("restic exit %d, command %d", code, got)
		}
		if code == 1 && (!strings.Contains(stderr, "[REDACTED]")) {
			t.Errorf("stderr %q", stderr)
		}
		for _, out := range append([]string{stdout, stderr}, h.log.lines...) {
			for _, secret := range []string{passMarker, "MARKER", urlMarker} {
				if strings.Contains(out, secret) {
					t.Errorf("exit %d: %s is exposed:\n%s", code, secret, out)
				}
			}
		}
	}
}

func TestASuccessfulRestoreWritesOneAuditLine(t *testing.T) {
	h := restoreHost(t)
	h.restore("aaaa1111")
	want := "repository base snapshot aaaa1111 restored to " + h.target()
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], want) || !strings.Contains(h.log.lines[0], "alice") {
		t.Errorf("audit %q, want %q", h.log.lines, want)
	}
}

func TestARefusalBeforeTheWritingBeginsWritesNoAuditLine(t *testing.T) {
	h := restoreHost(t)
	h.write(h.target()+"/keep.txt", "KEEP", 0o600)
	h.restore(idF)
	if len(h.log.lines) != 0 {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestAnAuditLineCannotBeSplitByTheTarget(t *testing.T) {
	h := restoreHost(t)
	target := h.path("a\nb")
	code, _, _ := h.sudo("repo", "restore", "base", "aaaa1111", "--target", target, "--config", "C")
	assertCode(t, code, exitOK)
	if len(h.log.lines) != 1 || strings.Contains(h.log.lines[0], "\n") {
		t.Errorf("audit %q", h.log.lines)
	}
}

// Rule "Справка есть у repo restore".

func TestRestoreHelp(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "restore", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"--target", "--overwrite", "--allow-root-target", "--path", "--source", "--timeout", "--connect-timeout", "latest",
		"sudo", "no Sard server", "registration", "under their original paths", "PostgreSQL", "  0  ", "  1  ", "  2  ", "  6  ", "  7  "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	h.assertNoBackendCalls()
}

// Р74, Р72, Р77: the help does not offer --overwrite as working, and names
// the new reasons.
func TestRestoreHelpSaysTheTargetIsEmptyOrNewAndOverwriteIsNotAvailable(t *testing.T) {
	h := newSetupHost(t)
	_, stdout, _ := h.sudo("repo", "restore", "--help")
	for _, want := range []string{"empty or new directory", "--overwrite is not available yet", "SETPRIV_MISSING", "ENV_FILE_INVALID", "BACKEND_NOT_SUPPORTED", "util-linux"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "files of the\n                             snapshot replace") || strings.Contains(stdout, "(together with --overwrite)") {
		t.Errorf("the help still offers --overwrite:\n%s", stdout)
	}
}

func TestAnAddressResticCannotReachIsRefusedBeforeTheTargetIsMade(t *testing.T) {
	h := restoreHost(t)
	url := "sftp:hostonly"
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAML("extra", url, h.path("secrets/base.pass"))), 0o640)
	h.restic.repo(url).snapshots = snapshotSet()
	code, _, stderr := h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "ADDRESS_INVALID")
	h.assertAbsent(h.target())
	if len(h.log.lines) != 0 {
		t.Errorf("audit %q", h.log.lines)
	}
}

func TestAConfigThatCannotBeReadIsAUsageError(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.sudo("repo", "restore", "base", idF, "--target", h.target(), "--config", h.path("missing.yaml"))
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "reading config") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}
