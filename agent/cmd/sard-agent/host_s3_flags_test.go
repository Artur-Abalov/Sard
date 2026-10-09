// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// The conventions of A8b (docs/specs/agent/host-setup.feature): A_S3, the
// key id KEY-ID-1 and the secret key S3-MARKER.
const (
	s3Address = "s3:https://s3.example.com/bucket-b/extra"
	keyID1    = "KEY-ID-1"
	s3Marker  = "S3-MARKER"
)

// s3Cmd is "repo add extra A_S3 --access-key-id KEY-ID-1 --secret-key-stdin"
// with the secret key on standard input, plus extra flags.
func (h *setupHost) s3Cmd(extra ...string) (int, string, string) {
	return h.s3At(s3Address, s3Marker, extra...)
}

func (h *setupHost) s3At(address, input string, extra ...string) (int, string, string) {
	h.stdinIs(input)
	args := append([]string{"repo", "add", "extra", address, "--access-key-id", keyID1, "--secret-key-stdin", "--config", "C"}, extra...)
	return h.sudo(args...)
}

// assertS3ValuesHidden is "значения не раскрыты" of A8b: S3-MARKER too.
func (h *setupHost) assertS3ValuesHidden(outputs ...string) {
	h.t.Helper()
	h.assertValuesHidden(outputs...)
	for _, out := range append(outputs, h.log.lines...) {
		if strings.Contains(out, s3Marker) {
			h.t.Fatalf("%s is exposed:\n%s", s3Marker, out)
		}
	}
}

// Rule "repo add проверяет адрес и флаги удалённого хранилища до обращения к нему".

func TestAnUnusableS3AddressIsRefusedBeforeTheStorageIsTouched(t *testing.T) {
	for _, address := range []string{
		"s3:https://s3.example.com",
		"s3:https://s3.example.com/",
		"s3:https:///bucket-b/extra",
		"s3:ftp://s3.example.com/bucket-b/extra",
		"s3:https://u:" + urlMarker + "@s3.example.com/bucket-b/extra",
		"s3:https://-s3.example.com/bucket-b/extra",
		"s3:https://s3.example.com/bucket-b/ex\ntra",
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.s3At(address, s3Marker)
		assertRefusal(t, code, stderr, exitUsage, "ADDRESS_INVALID")
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertS3ValuesHidden(stdout, stderr)
	}
}

func TestTheFormsOfAnS3AddressAreWrittenToTheFragmentAsGiven(t *testing.T) {
	for _, address := range []string{
		"s3:https://s3.example.com/bucket-b/extra",
		"s3:https://s3.example.com:9000/bucket-b",
		"s3:s3.example.com/bucket-b/extra",
		"s3:http://10.0.0.5:3900/bucket-b/extra",
	} {
		h := newSetupHost(t)
		code, _, stderr := h.s3At(address, s3Marker)
		assertCode(t, code, exitOK)
		if fragment := h.fileContent(h.path("agent.d/repo-extra.yaml")); !strings.Contains(fragment, "url: "+address+"\n") {
			t.Errorf("%s: fragment\n%s\nstderr %q", address, fragment, stderr)
		}
	}
}

func TestAnS3AddressOverHTTPIsAcceptedWithAWarningAboutMissingTLS(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.s3At("s3:http://10.0.0.5:3900/bucket-b/extra", s3Marker)
	assertCode(t, code, exitOK)
	if !strings.Contains(stderr, "without TLS") {
		t.Fatalf("stderr %q", stderr)
	}
	h2 := newSetupHost(t)
	if _, _, stderr := h2.s3Cmd(); strings.Contains(stderr, "TLS") {
		t.Fatalf("an https address is warned about: %q", stderr)
	}
}

func TestAFlagOfAnotherKindOfAddressIsRefusedWithItsName(t *testing.T) {
	for _, c := range []struct {
		address string
		flags   []string
		name    string
	}{
		{"LOCAL", []string{"--access-key-id", keyID1}, "--access-key-id"},
		{"LOCAL", []string{"--region", "ru-central1"}, "--region"},
		{"LOCAL", []string{"--secret-key-stdin"}, "--secret-key-stdin"},
		{"LOCAL", []string{"--secret-key-from-file", "F"}, "--secret-key-from-file"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		address := c.address
		if address == "LOCAL" {
			address = h.extraDir()
		}
		code, _, stderr := h.sudo(append([]string{"repo", "add", "extra", address, "--config", "C"}, c.flags...)...)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, c.name) {
			t.Errorf("%v: stderr %q", c.flags, stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
	}
}

func TestAnS3ConnectionWithoutAKeyIDIsRefusedWithTheFlagName(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	h.stdinIs(s3Marker)
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--secret-key-stdin", "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--access-key-id") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}

func TestAnUnusableKeyIDOrRegionIsRefusedWithTheFlagName(t *testing.T) {
	for _, c := range []struct {
		flags []string
		name  string
	}{
		{[]string{"--access-key-id", ""}, "--access-key-id"},
		{[]string{"--access-key-id", "KEY ID"}, "--access-key-id"},
		{[]string{"--access-key-id", strings.Repeat("A", 129)}, "--access-key-id"},
		{[]string{"--access-key-id", keyID1, "--region", "RU Central"}, "--region"},
		{[]string{"--access-key-id", keyID1, "--region", strings.Repeat("a", 65)}, "--region"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		h.stdinIs(s3Marker)
		code, _, stderr := h.sudo(append([]string{"repo", "add", "extra", s3Address, "--secret-key-stdin", "--config", "C"}, c.flags...)...)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, c.name) {
			t.Errorf("%v: stderr %q", c.flags, stderr)
		}
		h.assertHostUnchanged(before)
	}
}

func TestAKeyIDOf128CharactersAndARegionOf64CharactersAreAccepted(t *testing.T) {
	h := newSetupHost(t)
	h.stdinIs(s3Marker)
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--secret-key-stdin", "--config", "C",
		"--access-key-id", strings.Repeat("A", 128), "--region", strings.Repeat("a", 64))
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestAnUnusableConnectTimeoutIsRefusedWithTheFlagName(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "abc"} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.s3Cmd("--connect-timeout", value)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--connect-timeout") {
			t.Errorf("%s: stderr %q", value, stderr)
		}
		h.assertHostUnchanged(before)
	}
}

func TestTheSecretKeyIsNeverTheValueOfAFlagOrAnArgument(t *testing.T) {
	for _, args := range [][]string{
		{"repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key", s3Marker, "--config", "C"},
		{"repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key=" + s3Marker, "--config", "C"},
		{"repo", "add", "extra", s3Address, s3Marker, "--access-key-id", keyID1, "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sudo(args...)
		assertCode(t, code, exitUsage)
		h.assertS3ValuesHidden(stdout, stderr)
		h.assertHostUnchanged(before)
	}
}

func TestTheOtherKindsOfAddressStayRefusedAndNameTheSupportedOnes(t *testing.T) {
	for address, kind := range map[string]string{
		"rest:https://u:" + urlMarker + "@rest.example.com/extra": "rest",
		"b2:bucket:extra":        "b2",
		"azure:container:/extra": "azure",
		"gs:bucket:/extra":       "gs",
		"swift:container:/extra": "swift",
		"rclone:remote:extra":    "rclone",
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.sudo("repo", "add", "extra", address, "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "BACKEND_NOT_SUPPORTED")
		if !strings.Contains(stderr, kind) || !strings.Contains(stderr, "a local path, s3: or sftp:") {
			t.Errorf("stderr %q", stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertValuesHidden(stdout, stderr)
	}
}

// A8b-2 brings sftp:; until then it is refused and says so.
func TestAnSFTPAddressIsNotConnectedYet(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "add", "extra", "sftp:backup@nas.example.com:/srv/extra", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "BACKEND_NOT_SUPPORTED")
	if !strings.Contains(stderr, "sftp") || !strings.Contains(stderr, "not supported yet") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
}
