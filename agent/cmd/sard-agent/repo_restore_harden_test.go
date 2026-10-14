// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// Rules of repo restore "Р69" (the order of the checks), "Р73" (the staging
// directory), "Р79" (the target is held), "Р64" (--timeout) and the flags of
// each command: what each check says by itself, not only that something fails.

// openDescriptors are the files this process holds open whose path contains part.
func openDescriptors(t *testing.T, part string) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	ok(t, err)
	var held []string
	for _, e := range entries {
		if link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && strings.Contains(link, part) {
			held = append(held, link)
		}
	}
	return held
}

func TestTheRightsAreJudgedBeforeTheNameAndTheConfig(t *testing.T) {
	for name, run := range map[string]func(h *setupHost, args ...string) (int, string, string){
		"service user": (*setupHost).asService, "other": (*setupHost).asOther,
	} {
		h := restoreHost(t)
		code, _, stderr := run(h, "repo", "restore", "nosuch", idF, "--target", h.target(), "--config", "C")
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		assertReasonAbsent(t, stderr, "REPOSITORY_UNKNOWN")
		code, _, stderr = run(h, "repo", "restore", "base", idF, "--target", h.target(), "--config", h.path("no-such-config.yaml"))
		assertRefusal(t, code, stderr, exitUsage, "PRIVILEGES_REQUIRED")
		if n := strings.Count(stderr, "\n"); n != 1 {
			t.Errorf("%s: %d lines in %q", name, n, stderr)
		}
	}
}

func TestAKindOfStorageResticMayNotRunAsRootAgainstIsRefusedBeforeTheTargetIsJudged(t *testing.T) {
	h := restoreHost(t)
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAML("extra", "rclone:remote:x", h.path("secrets/base.pass"))), 0o640)
	ok(t, os.MkdirAll(h.target(), 0o755))
	h.write(filepath.Join(h.target(), "keep.txt"), "x", 0o644)
	code, _, stderr := h.sudo("repo", "restore", "extra", idF, "--target", h.target(), "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "BACKEND_NOT_SUPPORTED")
	assertReasonAbsent(t, stderr, "TARGET_NOT_EMPTY")
	h.assertNoBackendCalls()
}

func TestAMissingConfigOrRepositoryIsToldOnceAndNothingElseIsChecked(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target(), 0o755))
	h.write(filepath.Join(h.target(), "keep.txt"), "x", 0o644)
	missing := h.path("no-such-config.yaml")
	for name, args := range map[string][]string{
		"snapshots, config":     {"repo", "snapshots", "base", "--config", missing},
		"snapshots, repository": {"repo", "snapshots", "nosuch", "--config", "C"},
		"restore, config":       {"repo", "restore", "base", idF, "--target", h.target(), "--config", missing},
		"restore, repository":   {"repo", "restore", "nosuch", idF, "--target", h.target(), "--config", "C"},
	} {
		code, _, stderr := h.sudo(args...)
		if code != exitUsage || strings.Count(stderr, "\n") != 1 || strings.Contains(stderr, "TARGET_NOT_EMPTY") {
			t.Errorf("%s: code %d, stderr %q", name, code, stderr)
		}
		h.assertNoBackendCalls()
	}
}

func TestTheTargetIsNotHeldAfterACommandThatFailedBeforeItWrote(t *testing.T) {
	h := restoreHost(t)
	ok(t, os.MkdirAll(h.target(), 0o755))
	code, _, stderr := h.restore("ffffffff")
	assertRefusal(t, code, stderr, exitUsage, "SNAPSHOT_UNKNOWN")
	if held := openDescriptors(t, h.target()); len(held) != 0 {
		t.Errorf("still open: %v", held)
	}
	code, _, _ = h.restore(idF)
	assertCode(t, code, exitOK)
}

func TestTheStagingDirectoryNameIsMadeOfEightRandomBytes(t *testing.T) {
	h := restoreHost(t)
	random := &countingReader{}
	h.deps.random = random
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	if random.n != 8 {
		t.Errorf("%d random bytes were read", random.n)
	}
}

// countingReader gives 1, 2, 3, ... and counts what it gave.
type countingReader struct{ n int }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		c.n++
		p[i] = byte(c.n)
	}
	return len(p), nil
}

func TestTheRestoredPathsAreListedOnlyWhenTheyWereAskedFor(t *testing.T) {
	h := restoreHost(t)
	_, stdout, _ := h.restore(idF)
	if strings.Contains(stdout, "paths:") {
		t.Errorf("stdout %q", stdout)
	}
	h = restoreHost(t)
	_, stdout, _ = h.restore(idF, "--path", "/srv/data/sub")
	if !strings.Contains(stdout, "  paths:    [/srv/data/sub]\n") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestASnapshotWithoutPathsGivesNoExampleOfWhereTheFilesLie(t *testing.T) {
	h := restoreHost(t)
	for i := range h.base().snapshots {
		if h.base().snapshots[i].ID == idF {
			h.base().snapshots[i].Paths = nil
		}
	}
	code, stdout, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	if strings.Contains(stdout, "for example") || !strings.Contains(stdout, "restored: ") {
		t.Errorf("stdout %q", stdout)
	}
}

func TestAnyTimeoutOfOneNanosecondStillBoundsTheCommandAndNoTimeoutDoesNot(t *testing.T) {
	clock := newFakeEnrollClock()
	ctx, cancel := repoContext(context.Background(), clock, time.Nanosecond)
	defer cancel(nil)
	if d := clock.waitTimer(t); d != time.Nanosecond {
		t.Fatalf("timer of %v", d)
	}
	clock.fireNow()
	select {
	case <-ctx.Done():
		if context.Cause(ctx) != repoinit.ErrTimeout {
			t.Errorf("cause %v", context.Cause(ctx))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command was not bounded")
	}
	free, cancelFree := repoContext(context.Background(), clock, 0)
	defer cancelFree(nil)
	select {
	case d := <-clock.added:
		t.Errorf("a timer of %v was set for no limit", d)
	default:
	}
	if free.Err() != nil {
		t.Errorf("err %v", free.Err())
	}
}

func TestAFlagACommandDoesNotOfferIsRefused(t *testing.T) {
	for _, c := range []struct {
		parse func(string, []string, *bytes.Buffer) int
		sub   string
		args  []string
	}{
		{repoParse, "list", []string{"--password-stdin"}}, {repoParse, "list", []string{"--password-from-file", "f"}},
		{repoParse, "list", []string{"--region", "r"}}, {repoParse, "list", []string{"--connect-timeout", "1s"}},
		{repoParse, "list", []string{"--source", "s"}}, {repoParse, "list", []string{"--target", "/t"}},
		{repoParse, "init", []string{"--from-file", "f", "base"}}, {repoParse, "init", []string{"--region", "r", "base"}},
		{repoParse, "snapshots", []string{"--region", "r", "base"}}, {repoParse, "snapshots", []string{"--password-stdin", "base"}},
		{repoParse, "snapshots", []string{"--password-from-file", "f", "base"}},
		{repoParse, "add", []string{"--source", "s", "a", "b"}}, {repoParse, "add", []string{"--from-file", "f", "a", "b"}},
		{secretParse, "list", []string{"--from-file", "f"}}, {secretParse, "remove", []string{"--from-file", "f", "x"}},
		{secretParse, "remove", []string{"--source", "s", "x"}}, {secretParse, "remove", []string{"--connect-timeout", "1s", "x"}},
		{secretParse, "remove", []string{"--region", "r", "x"}}, {secretParse, "remove", []string{"--password-stdin", "x"}},
	} {
		var stderr bytes.Buffer
		if code := c.parse(c.sub, c.args, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "not defined") {
			t.Errorf("%s %v: code %d, stderr %q", c.sub, c.args, code, stderr.String())
		}
	}
}

func repoParse(sub string, args []string, stderr *bytes.Buffer) int {
	_, code := parseRepoFlags(sub, args, stderr, hostDeps{})
	return code
}

func secretParse(sub string, args []string, stderr *bytes.Buffer) int {
	_, code := parseSecretFlags(sub, args, stderr, hostDeps{})
	return code
}

func TestSecretListTakesNoName(t *testing.T) {
	var stderr bytes.Buffer
	if code := secretParse("list", []string{"extra"}, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "unexpected extra argument") {
		t.Errorf("code %d, stderr %q", code, stderr.String())
	}
}

func TestADurationOfOneNanosecondIsPositive(t *testing.T) {
	var stderr bytes.Buffer
	opts, code := parseRepoFlags("restore", []string{"base", idF, "--target", "/t", "--timeout", "1ns", "--connect-timeout", "1ns"}, &stderr, hostDeps{})
	if code != exitOK || opts.timeout != time.Nanosecond || opts.connectTimeout != time.Nanosecond {
		t.Errorf("code %d, options %+v, stderr %q", code, opts, stderr.String())
	}
}

func TestTheFlagValuesOfNoFlagAreEmptyText(t *testing.T) {
	var list *stringList
	if got := list.String(); got != "" {
		t.Errorf("list %q", got)
	}
	if got := (positiveDuration{}).String(); got != "" {
		t.Errorf("duration %q", got)
	}
}

func TestABadFlagOfRepoSnapshotsIsTheOnlyThingSaid(t *testing.T) {
	h := restoreHost(t)
	code, _, stderr := h.sudo("repo", "snapshots", "base", "--no-such-flag", "--config", "C")
	assertCode(t, code, exitUsage)
	if strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, "no-such-flag") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNoBackendCalls()
}

func TestATargetThatCannotBeMadeIsATargetWriteFailureAndNothingIsRestored(t *testing.T) {
	h := restoreHost(t)
	h.fsys.failOn, h.fsys.failPath, h.fsys.failErr = "mkdir", filepath.Base(h.target()), syscall.ENOSPC
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitWrite, "TARGET_WRITE")
	if !strings.Contains(stderr, "cannot create the target") || len(h.restoreCalls()) != 0 {
		t.Errorf("stderr %q", stderr)
	}
	h.assertAbsent(h.target())
}
