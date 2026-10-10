// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"testing"
)

// Rule "Ключ SSH пользователя службы создаётся от его имени и показывается открытой частью".

func TestWithoutAKeyOneIsMadeByTheKeygenAsTheServiceUser(t *testing.T) {
	h := newSFTPHost(t)
	code, _, stderr := h.sftpCmd()
	assertCode(t, code, exitOK)
	_ = stderr
	runs := h.ssh.called("ssh-keygen")
	if len(runs) != 1 {
		t.Fatalf("ssh-keygen ran %d times", len(runs))
	}
	want := []string{"-q", "-t", "ed25519", "-N", "", "-C", "sard-agent@host1", "-f", h.sshFile("id_ed25519")}
	if !slices.Equal(runs[0].args, want) {
		t.Fatalf("args %q, want %q", runs[0].args, want)
	}
	if runs[0].runAs == nil || runs[0].runAs.UID != serviceUID || runs[0].runAs.GID != serviceUID {
		t.Fatalf("ssh-keygen ran as %+v", runs[0].runAs)
	}
	got := strings.Join(h.log.lines, "\n")
	for _, want := range []string{"ssh key of service user sard-agent created", "ssh host key of nas.example.com trusted", "repository extra added"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit lacks %q:\n%s", want, got)
		}
	}
}

func TestThePublicPartIsPrintedWithWhereToAddIt(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, stdout, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	for _, want := range []string{"ssh-ed25519 AAAAexistingkey sard-agent@host1", "authorized_keys", "backup", sftpHost} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestAnExistingKeyIsNotMadeAgain(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	private, public := h.fileContent(h.sshFile("id_ed25519")), h.fileContent(h.sshFile("id_ed25519.pub"))
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	if len(h.ssh.called("ssh-keygen")) != 0 || h.fileContent(h.sshFile("id_ed25519")) != private || h.fileContent(h.sshFile("id_ed25519.pub")) != public {
		t.Fatal("the key was made again")
	}
	for _, l := range h.log.lines {
		if strings.Contains(l, "ssh key of service user") {
			t.Fatalf("audit %q", h.log.lines)
		}
	}
}

func TestAPrivateKeyWithoutAPublicPartGetsItFromTheKeygenAsTheServiceUserWithoutAFile(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	ok(t, removeFile(h.sshFile("id_ed25519.pub")))
	code, stdout, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	runs := h.ssh.called("ssh-keygen")
	if len(runs) != 1 || runs[0].args[0] != "-y" || runs[0].runAs == nil || runs[0].runAs.UID != serviceUID {
		t.Fatalf("ssh-keygen: %+v", runs)
	}
	if !strings.Contains(stdout, "ssh-ed25519 AAAAderivedkey sard-agent@host1") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertAbsent(h.sshFile("id_ed25519.pub"))
}

func TestTheKeyIsMadeOnlyAfterTheHostKeyIsConfirmed(t *testing.T) {
	h := newSFTPHost(t)
	h.stdin.Reset()
	before := h.sshTree()
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "HOST_KEY_UNCONFIRMED")
	if len(h.ssh.called("ssh-keygen")) != 0 {
		t.Fatal("ssh-keygen ran")
	}
	h.assertSSHUnchanged(before)
}

// Rule "Ключ хоста SFTP никогда не принимается молча".

func TestAFingerprintOfTheFlagThatMatchesIsTrustedWithoutAQuestion(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.putSSH("known_hosts", "other.example.com ssh-ed25519 AAAA\n", 0o600)
	term := h.terminalIs()
	code, stdout, _ := h.sftpAt(sftpAddress, "--host-key-fingerprint", keyEC.fingerprint())
	assertCode(t, code, exitOK)
	if len(term.prompts) != 0 {
		t.Fatalf("the terminal was asked: %q", term.prompts)
	}
	if want := "other.example.com ssh-ed25519 AAAA\n" + keyEC.line(sftpHost); h.fileContent(h.sshFile("known_hosts")) != want {
		t.Fatalf("known_hosts %q", h.fileContent(h.sshFile("known_hosts")))
	}
	if !strings.Contains(stdout, sftpHost) || !strings.Contains(stdout, keyEC.fingerprint()) {
		t.Fatalf("stdout %q", stdout)
	}
	if !strings.Contains(strings.Join(h.log.lines, "\n"), "ssh host key of nas.example.com trusted ecdsa-sha2-nistp256 "+keyEC.fingerprint()) {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestTheScanRunsAsTheServiceUserWithThePortOfTheAddress(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	code, _, stderr := h.sftpAt("sftp://backup@nas.example.com:2222//srv/extra", "--host-key-fingerprint", keyED.fingerprint())
	assertCode(t, code, exitOK)
	_ = stderr
	scans := h.ssh.called("ssh-keyscan")
	if len(scans) != 1 || scans[0].runAs == nil || scans[0].runAs.UID != serviceUID || scans[0].runAs.GID != serviceUID {
		t.Fatalf("scans %+v", scans)
	}
	if !slices.Contains(scans[0].args, "2222") || scans[0].args[len(scans[0].args)-1] != sftpHost {
		t.Fatalf("args %q", scans[0].args)
	}
	if got := h.fileContent(h.sshFile("known_hosts")); !strings.Contains(got, "[nas.example.com]:2222 ssh-ed25519") {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestTheEntryOfTheKnownHostHasTheFormSSHUsesForThePortAndForIPv6(t *testing.T) {
	for address, start := range map[string]string{
		"sftp:backup@nas.example.com:/srv/extra":      "nas.example.com ssh-ed25519",
		"sftp://backup@nas.example.com:22//srv/extra": "nas.example.com ssh-ed25519",
		"sftp://backup@[2001:db8::1]//srv/extra":      "2001:db8::1 ssh-ed25519",
		"sftp://backup@[2001:db8::1]:2222//srv/extra": "[2001:db8::1]:2222 ssh-ed25519",
	} {
		h := newSFTPHost(t)
		code, _, stderr := h.sftpAt(address, "--host-key-fingerprint", keyED.fingerprint())
		assertCode(t, code, exitOK)
		_ = stderr
		if got := h.fileContent(h.sshFile("known_hosts")); !strings.HasPrefix(got, start) {
			t.Errorf("%s: known_hosts %q", address, got)
		}
	}
}

func TestAFingerprintThatMatchesNoKeyIsAMismatchOfTrustAndWritesNothing(t *testing.T) {
	h := newSFTPHost(t)
	before := h.sshTree()
	other := "SHA256:" + strings.Repeat("A", 43)
	code, _, stderr := h.sftpAt(sftpAddress, "--host-key-fingerprint", other)
	assertRefusal(t, code, stderr, exitTrust, "HOST_KEY_MISMATCH")
	for _, want := range []string{other, keyED.fingerprint(), keyEC.fingerprint()} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q", want)
		}
	}
	if len(h.ssh.called("ssh-keygen")) != 0 {
		t.Error("ssh-keygen ran")
	}
	h.assertNoBackendCalls()
	h.assertNothingChanged(before)
}

func TestAtTheTerminalOneKeyIsShownAndYesTrustsIt(t *testing.T) {
	h := newSFTPHost(t)
	term := h.terminalIs("yes")
	code, _, stderr := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	_ = stderr
	if len(term.prompts) != 1 {
		t.Fatalf("prompts %q", term.prompts)
	}
	for _, want := range []string{sftpHost, "22", "ssh-ed25519", keyED.fingerprint(), "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"} {
		if !strings.Contains(term.prompts[0], want) {
			t.Errorf("the prompt lacks %q:\n%s", want, term.prompts[0])
		}
	}
	if got := h.fileContent(h.sshFile("known_hosts")); got != keyED.line(sftpHost) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestAServerWithoutAnED25519KeyOffersTheECDSAKeyAtTheTerminal(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.keys = []serverKey{keyRSA, keyEC}
	term := h.terminalIs("yes")
	code, _, _ := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	if len(term.prompts) != 1 || !strings.Contains(term.prompts[0], "ecdsa-sha2-nistp256") || !strings.Contains(term.prompts[0], keyEC.fingerprint()) {
		t.Fatalf("prompts %q", term.prompts)
	}
	if got := h.fileContent(h.sshFile("known_hosts")); got != keyEC.line(sftpHost) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestAnAnswerOtherThanYesIsRejectedAndNothingIsWritten(t *testing.T) {
	for _, answers := range [][]string{{"no"}, {"y"}, {"YES"}, {""}, {}} {
		h := newSFTPHost(t)
		h.terminalIs(answers...)
		before := h.sshTree()
		code, _, stderr := h.sftpAt(sftpAddress)
		assertRefusal(t, code, stderr, exitTrust, "HOST_KEY_REJECTED")
		if len(h.ssh.called("ssh-keygen")) != 0 {
			t.Errorf("%q: ssh-keygen ran", answers)
		}
		h.assertNothingChanged(before)
	}
}

func TestWithoutATerminalAndWithoutAFlagTheHostKeyIsUnconfirmedAndNothingIsRead(t *testing.T) {
	h := newSFTPHost(t)
	h.deps.stdin = &hungInput{}
	before := h.sshTree()
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "HOST_KEY_UNCONFIRMED")
	for _, want := range []string{keyED.fingerprint(), keyEC.fingerprint(), "--host-key-fingerprint"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if reads := h.deps.stdin.(*hungInput).reads; reads != 0 {
		t.Fatalf("standard input was read %d times", reads)
	}
	h.assertNothingChanged(before)
}

func TestAKnownHostKeyIsNeitherAskedNorWrittenAgain(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	term := h.terminalIs()
	known := h.fileContent(h.sshFile("known_hosts"))
	code, _, _ := h.sftpAt(sftpAddress)
	assertCode(t, code, exitOK)
	if len(term.prompts) != 0 || h.fileContent(h.sshFile("known_hosts")) != known {
		t.Fatalf("prompts %q", term.prompts)
	}
	for _, l := range h.log.lines {
		if strings.Contains(l, "ssh host key") {
			t.Fatalf("audit %q", h.log.lines)
		}
	}
}

func TestAHashedEntryOfTheKnownHostIsRecognised(t *testing.T) {
	h := newSFTPHost(t)
	entry := hashedHost(sftpHost) + " " + keyED.keyType + " " + keyED.blob + "\n"
	h.putSSH("known_hosts", entry, 0o600)
	h.putSSH("id_ed25519", "PRIVATE-KEY-MARKER", 0o600)
	h.putSSH("id_ed25519.pub", "ssh-ed25519 AAAAexistingkey x\n", 0o644)
	code, _, _ := h.sftpAt(sftpAddress, "--host-key-fingerprint", keyED.fingerprint())
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.sshFile("known_hosts")); got != entry {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestAnEntryOfAnotherPortOfTheSameHostIsNotTheKnownKey(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyED.line("[nas.example.com]:2222"), 0o600)
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.sshFile("known_hosts")); got != keyED.line("[nas.example.com]:2222")+keyED.line(sftpHost) {
		t.Fatalf("known_hosts %q", got)
	}
}

func TestAChangedHostKeyIsRefusedWithoutReplacingIt(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyRSA.line("other.example.com")+"\n"+keyOldED.line(sftpHost), 0o600)
	before := h.sshTree()
	code, _, stderr := h.sftpCmd()
	assertRefusal(t, code, stderr, exitTrust, "HOST_KEY_CHANGED")
	for _, want := range []string{h.sshFile("known_hosts"), "3", "--replace-host-key", "substitution", "reinstall"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	h.assertNothingChanged(before)
}

func TestTheFlagToReplaceReplacesAChangedHostKeyWithTheConfirmedOne(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyOldED.line(sftpHost)+keyRSA.line("other.example.com"), 0o600)
	code, _, _ := h.sftpCmd("--replace-host-key")
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.sshFile("known_hosts")); got != keyRSA.line("other.example.com")+keyED.line(sftpHost) {
		t.Fatalf("known_hosts %q", got)
	}
	if !strings.Contains(strings.Join(h.log.lines, "\n"), "ssh host key of nas.example.com replaced ssh-ed25519 "+keyED.fingerprint()) {
		t.Fatalf("audit %q", h.log.lines)
	}
}

func TestTheFlagToReplaceDoesNotSkipTheConfirmation(t *testing.T) {
	h := newSFTPHost(t)
	h.putSSH("known_hosts", keyOldED.line(sftpHost), 0o600)
	h.stdin.Reset()
	before := h.sshTree()
	code, _, stderr := h.sftpAt(sftpAddress, "--replace-host-key")
	assertRefusal(t, code, stderr, exitUsage, "HOST_KEY_UNCONFIRMED")
	h.assertSSHUnchanged(before)
}

func TestAHostKeyThatSSHRefusesAtTheLoginIsAMismatchOfTrust(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.srv.loginStderr, h.srv.loginCode = "Host key verification failed.", 255
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitTrust, "HOST_KEY_MISMATCH")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

// Rule "Конфиг ssh пользователя службы получает управляемый блок хоста".

const hostBlock = `# sard-agent begin nas.example.com
Host nas.example.com
    StrictHostKeyChecking yes
    BatchMode yes
    IdentityFile ~/.ssh/id_ed25519
    IdentitiesOnly yes
    ServerAliveInterval 15
    ServerAliveCountMax 4
    ConnectTimeout 30
# sard-agent end nas.example.com
`

func TestWithoutAConfigItIsMadeOfOneBlockOfTheHost(t *testing.T) {
	h := newSFTPHost(t)
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.sshFile("config")); got != hostBlock {
		t.Fatalf("config %q", got)
	}
	h.assertOwner(h.sshFile("config"), serviceUID)
	h.assertMode(h.sshFile("config"), 0o600)
}

func TestTheBlockStandsBeforeTheFormerContentOfTheConfig(t *testing.T) {
	h := newSFTPHost(t)
	old := "Host *\n    ServerAliveInterval 0\n"
	h.putSSH("config", old, 0o600)
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	got := h.fileContent(h.sshFile("config"))
	if !strings.HasPrefix(got, hostBlock) || !strings.HasSuffix(got, old) {
		t.Fatalf("config %q", got)
	}
}

func TestTheSameBlockDoesNotRewriteTheConfig(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.putSSH("config", hostBlock, 0o600)
	h.fsys.events = nil
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	for _, e := range h.fsys.events {
		if strings.Contains(e, "/config") {
			t.Fatalf("config was written: %v", h.fsys.events)
		}
	}
}

func TestAnOutdatedBlockOfTheSameHostIsReplacedInPlace(t *testing.T) {
	h := newSFTPHost(t)
	old := strings.Replace(hostBlock, "ServerAliveInterval 15", "ServerAliveInterval 60", 1) + "Host mine\n    User me\n"
	h.putSSH("config", old, 0o600)
	code, _, _ := h.sftpCmd()
	assertCode(t, code, exitOK)
	if got := h.fileContent(h.sshFile("config")); got != hostBlock+"Host mine\n    User me\n" {
		t.Fatalf("config %q", got)
	}
}

func TestBlocksOfDifferentHostsLiveSideBySide(t *testing.T) {
	h := newSFTPHost(t)
	h.srv.keys = []serverKey{keyED, keyEC}
	code, _, _ := h.sftpAt("sftp:backup@other.example.com:/srv/other", "--host-key-fingerprint", keyED.fingerprint())
	assertCode(t, code, exitOK)
	code, _, stderr := h.sudo("repo", "add", "second", sftpAddress, "--host-key-fingerprint", keyED.fingerprint(), "--config", "C")
	assertCode(t, code, exitOK)
	_ = stderr
	got := h.fileContent(h.sshFile("config"))
	if strings.Count(got, "# sard-agent begin other.example.com") != 1 || strings.Count(got, "# sard-agent begin nas.example.com") != 1 {
		t.Fatalf("config %q", got)
	}
}

func TestEveryProgramOfTheClientRunsWithoutQuestionsAtTheTerminal(t *testing.T) {
	h := newSFTPHost(t)
	h.keyAndHostKnown()
	h.srv.authorized = false
	term := h.terminalIs()
	code, _, stderr := h.sftpAt(sftpAddress)
	assertRefusal(t, code, stderr, exitUsage, "SSH_KEY_NOT_AUTHORIZED")
	if len(term.prompts) != 0 {
		t.Fatalf("the terminal was asked: %q", term.prompts)
	}
	for _, c := range h.ssh.calls {
		if c.program != "sftp" {
			continue
		}
		args := strings.Join(c.args, " ")
		if !strings.Contains(args, "StrictHostKeyChecking=yes") || !strings.Contains(args, "BatchMode=yes") {
			t.Errorf("%s ran without the options: %s", c.program, args)
		}
	}
}

func removeFile(path string) error { return os.Remove(path) }

// hashedHost is the |1|salt|hash form ssh -H writes for a host.
func hashedHost(host string) string {
	salt := base64.StdEncoding.EncodeToString([]byte("0123456789abcdefghij"))
	mac := hmac.New(sha1.New, []byte("0123456789abcdefghij"))
	mac.Write([]byte(host))
	return "|1|" + salt + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
