// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
)

// docs/specs/agent/restic-keys.feature, rules "Повторный выпуск с той же меткой отклоняется или ничего не делает"
// and "После выпуска команда печатает следующие шаги".

func withKeyOf(h *keysHost, user, host string) {
	h.keys(keyH(), keyR(), fakeKey{id: keyID("X"), user: user, host: host, password: "X-PASSWORD", created: at(5)})
}

func TestAKeyWithTheSamePurposeAndLabelIsRefusedWithAHint(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH(), keyR(), keyV())
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-1")
	assertRefusal(t, code, stderr, exitUsage, "KEY_EXISTS")
	assertReason(t, stderr, "--label")
	assertReason(t, stderr, "--if-missing")
	h.assertNoKeyAdd()
}

func TestASecondRecoveryKeyWithoutALabelIsRefused(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, _, stderr := h.keyCmd("add", "base", "--for", "recovery")
	assertReason(t, stderr, "KEY_EXISTS")
	h.assertNoKeyAdd()
}

func TestWithIfMissingAnExistingKeyIsSuccessWithoutChanges(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH(), keyR(), keyV())
	file := h.path("out/vk")
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, stdout, _ := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-1", "--if-missing", "--to-file", file)
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h.assertNoKeyAdd()
	h.assertAbsent(file)
	if len(h.log.lines) != 0 {
		t.Errorf("audit lines: %q", h.log.lines)
	}
}

func TestWithIfMissingAMissingKeyIsIssued(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-2", "--if-missing")
	assertCode(t, code, exitOK)
	if h.added().user != "sard:verifier:vrf-2" {
		t.Errorf("user %q", h.added().user)
	}
}

func TestTheSamePurposeWithAnotherLabelIsIssued(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.keys(keyH(), keyR(), keyV())
	code, _, _ := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-2")
	assertCode(t, code, exitOK)
	if h.added().user != "sard:verifier:vrf-2" {
		t.Errorf("user %q", h.added().user)
	}
}

func TestTheLabelOfAKeyIssuedOnAnotherHostIsTakenToo(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	withKeyOf(h, "sard:verifier:vrf-1", "src2")
	_, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--label", "vrf-1")
	assertReason(t, stderr, "KEY_EXISTS")
}

func TestAfterAVerifierKeyTheRepoAddCommandForTheVerifierHostIsPrinted(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.cfg.Repositories[0].URL = "s3:https://s3.example.net/acme/base"
	h.saveConfig()
	r := h.restic.repo("s3:https://s3.example.net/acme/base")
	r.initialized, r.password = true, passMarker
	r.keyring.keys = []fakeKey{keyH(), keyR()}
	code, stdout, _ := h.keyCmd("add", "base", "--for", "verifier")
	assertCode(t, code, exitOK)
	for _, want := range []string{"sudo sard-agent repo add base s3:https://s3.example.net/acme/base --password-from-file", "read-only", "--access-key-id"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestTheAddressInTheCommandForTheVerifierHoldsNoPassword(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"sftp:backup@nas.example.com:/srv/base":               "sftp:backup@nas.example.com:/srv/base",
		"sftp://backup:URL-MARKER@nas.example.com:2222/srv/b": "sftp://backup@nas.example.com:2222/srv/b",
		"rest:https://u:URL-MARKER@rest.example.net/base":     "rest:https://u@rest.example.net/base",
	} {
		h := newKeysHost(t)
		h.cfg.Repositories[0].URL = url
		h.saveConfig()
		r := h.restic.repo(url)
		r.initialized, r.password = true, passMarker
		r.keyring.keys = []fakeKey{keyH(), keyR()}
		code, stdout, stderr := h.keyCmd("add", "base", "--for", "verifier")
		assertCode(t, code, exitOK)
		if !strings.Contains(stdout, "sudo sard-agent repo add base "+want) || strings.Contains(stdout+stderr, urlMarker) {
			t.Errorf("%s: stdout:\n%s", url, stdout)
		}
	}
}

func TestAVerifierKeyForALocalPathWarnsThatTheVerifierMustSeeTheSameStorage(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	code, stdout, _ := h.keyCmd("add", "base", "--for", "verifier")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "must see the same storage") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestAfterARecoveryKeyItIsSaidWhereToKeepIt(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, stdout, _ := h.keyCmd("add", "base", "--for", "recovery", "--label", "x")
	for _, want := range []string{"outside this infrastructure", "without a recovery key and without the password of this host"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestAfterAKeyOfAnotherPurposeOnlyItsIDIsPrinted(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	_, stdout, _ := h.keyCmd("add", "base", "--for", "other", "--label", "auditor")
	id := h.base().keyring.keys[len(h.base().keyring.keys)-1].id[:8]
	if !strings.Contains(stdout, id) || strings.Contains(stdout, "sard-agent repo add") {
		t.Errorf("stdout:\n%s", stdout)
	}
}
