// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "repo add проверяет адрес и флаги удалённого хранилища до обращения к нему" (SFTP).

func TestAnUnusableSFTPAddressIsRefusedWithoutRunningSSH(t *testing.T) {
	for _, address := range []string{
		"sftp:backup@nas.example.com",
		"sftp:",
		"sftp://backup:" + urlMarker + "@nas.example.com//srv/extra",
		"sftp://backup@nas.example.com:0//srv/extra",
		"sftp://backup@nas.example.com:65536//srv/extra",
		"sftp:-oProxyCommand=touch-pwned:/srv/extra",
		"sftp:-lroot@nas.example.com:/srv/extra",
		"sftp:backup@nas.example.com:/srv/ex\ntra",
	} {
		h := newSFTPHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sftpAt(address, "--host-key-fingerprint", keyED.fingerprint())
		assertRefusal(t, code, stderr, exitUsage, "ADDRESS_INVALID")
		h.assertNoSSHProgram()
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertSFTPValuesHidden(stdout, stderr)
	}
}

func TestAHostKeyFingerprintNotInTheFormOfSHA256IsRefusedWithTheFlagName(t *testing.T) {
	for _, fingerprint := range []string{
		"MD5:16:27:ac:a5:76:28:2d:36:63:1b:56:4d:eb:df:a6:48",
		"SHA256:short",
		keyED.fingerprint() + "=",
		strings.TrimPrefix(keyED.fingerprint(), "SHA256:"),
	} {
		h := newSFTPHost(t)
		before := h.hostTree()
		code, _, stderr := h.sftpAt(sftpAddress, "--host-key-fingerprint", fingerprint)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--host-key-fingerprint") || !strings.Contains(stderr, "SHA256:") {
			t.Errorf("%q: stderr %q", fingerprint, stderr)
		}
		h.assertNoSSHProgram()
		h.assertHostUnchanged(before)
	}
}
