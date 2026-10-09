// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Предустановка провайдера строит адрес хранилища из региона" (A8b-3, Р50).

func TestAProviderPresetBuildsTheAddressAndTheRegion(t *testing.T) {
	for _, c := range []struct{ provider, region, url string }{
		{"aws", "eu-central-1", "s3:https://s3.eu-central-1.amazonaws.com/bucket-b/extra"},
		{"b2", "us-west-004", "s3:https://s3.us-west-004.backblazeb2.com/bucket-b/extra"},
	} {
		h := newSetupHost(t)
		code, _, stderr := h.s3At("s3:bucket-b/extra", s3Marker, "--provider", c.provider, "--region", c.region)
		assertCode(t, code, exitOK)
		if fragment := h.fileContent(h.path("agent.d/repo-extra.yaml")); !strings.Contains(fragment, "url: "+c.url+"\n") {
			t.Errorf("%s: fragment\n%s\nstderr %q", c.provider, fragment, stderr)
		}
		if env := h.fileContent(h.envPath()); !strings.Contains(env, "AWS_DEFAULT_REGION="+c.region+"\n") {
			t.Errorf("%s: env file lacks the region", c.provider)
		}
	}
}

func TestAProviderPresetWithoutAPathWritesTheAddressOfTheBucket(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3At("s3:bucket-b", s3Marker, "--provider", "aws", "--region", "us-east-1")
	assertCode(t, code, exitOK)
	if fragment := h.fileContent(h.path("agent.d/repo-extra.yaml")); !strings.Contains(fragment, "url: s3:https://s3.us-east-1.amazonaws.com/bucket-b\n") {
		t.Fatalf("fragment\n%s", fragment)
	}
}

func TestAnUnusableProviderCombinationIsAUsageErrorWithoutTouchingTheStorage(t *testing.T) {
	for _, c := range []struct {
		address string
		flags   []string
		names   string
	}{
		{"s3:bucket-b/extra", []string{"--provider", "yandex", "--region", "ru-central1"}, "aws, b2"},
		{"s3:bucket-b/extra", []string{"--provider", "aws"}, "--region"},
		{"s3:bucket-b/extra", []string{"--provider", "b2"}, "--region"},
		{s3Address, []string{"--provider", "aws", "--region", "eu-central-1"}, "host"},
		{"s3:https://bucket-b", []string{"--provider", "aws", "--region", "eu-central-1"}, "scheme"},
		{"s3:bucket-b/extra", []string{"--provider", "aws", "--region", "EU Central"}, "--region"},
		{"s3:", []string{"--provider", "aws", "--region", "eu-central-1"}, "bucket"},
		{"LOCAL", []string{"--provider", "aws", "--region", "eu-central-1"}, "--provider"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		address := c.address
		if address == "LOCAL" {
			address = h.extraDir()
		}
		code, stdout, stderr := h.s3At(address, s3Marker, c.flags...)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, c.names) {
			t.Errorf("%s %v: stderr %q lacks %q", address, c.flags, stderr, c.names)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertS3ValuesHidden(stdout, stderr)
	}
}

func TestARegionThePresetDoesNotKnowFailsAtTheStorageWithItsAddress(t *testing.T) {
	h := newSetupHost(t)
	url := "s3:https://s3.xx-nowhere-9.amazonaws.com/bucket-b/extra"
	h.restic.repo(url).answers("cat", "Fatal: unable to open config file: Stat: Get \"https://s3.xx-nowhere-9.amazonaws.com/bucket-b/extra/config\": dial tcp: lookup s3.xx-nowhere-9.amazonaws.com: no such host", 1)
	code, _, stderr := h.s3At("s3:bucket-b/extra", s3Marker, "--provider", "aws", "--region", "xx-nowhere-9")
	assertRefusal(t, code, stderr, exitTemporary, "BACKEND_UNAVAILABLE")
	if !strings.Contains(stderr, url) {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
}

func TestRepeatingAProviderPresetChangesNothing(t *testing.T) {
	h := newSetupHost(t)
	preset := []string{"--provider", "aws", "--region", "eu-central-1"}
	code, _, _ := h.s3At("s3:bucket-b/extra", s3Marker, preset...)
	assertCode(t, code, exitOK)
	h.sd.calls, h.log.lines = nil, nil
	before := h.hostTree()
	code, stdout, _ := h.sudo(append([]string{"repo", "add", "extra", "s3:bucket-b/extra", "--access-key-id", keyID1, "--config", "C"}, preset...)...)
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "unchanged") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertHostUnchanged(before)
}

func TestRepoAddHelpListsTheProvidersAndRequiresTheRegionWithThem(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "add", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"--provider", "aws", "b2", "--provider needs --region", "s3:<bucket>[/<path>]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}
