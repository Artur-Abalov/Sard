// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// The subcommand is dispatched from the command line of the binary itself.
func TestRepoIsDispatchedFromTheAgentCommandLine(t *testing.T) {
	code, stdout, _ := runAgent("repo", "list", "--help")
	if code != exitOK || !strings.Contains(stdout, "Usage: sard-agent repo list") {
		t.Fatalf("list --help: code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = runAgent("repo", "init", "--help")
	if code != exitOK || !strings.Contains(stdout, "Usage: sard-agent repo init") {
		t.Fatalf("init --help: code = %d, stdout = %q", code, stdout)
	}
	code, stdout, stderr := runAgent("repo")
	if code != exitUsage || stdout != "" || stderr != "sard-agent repo: want a subcommand: init, list, add, show, remove or password\n" {
		t.Fatalf("repo alone: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// A subcommand alone, with no flag and no name, is a complete command line.
func TestRepoListWithoutAnyArgumentUsesTheDefaultConfig(t *testing.T) {
	h := newRepoHost(t)
	h.deps.defaultConfig = h.cfgPath
	h.main().initialized = true
	code, stdout, stderr := h.run("list")
	assertCode(t, code, exitOK)
	if rows := listRows(t, stdout); rows["main"][2] != "initialized" || stderr != "" {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
}

func TestEachUsageErrorSaysWhatIsWrong(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"init", "--config", "C"}, "sard-agent repo init: the name of a repository of the agent config is required: sard-agent repo init [flags] <name>\n"},
		{[]string{"init", "--config", "C", "main", "extra"}, "sard-agent repo init: unexpected extra argument\n"},
		{[]string{"init", "--config", "C", "--insecure", "main"}, "sard-agent repo init: flag provided but not defined: -insecure\n"},
		{[]string{"list", "--config", "C", "--json", "main"}, "sard-agent repo list: unexpected extra argument\n"},
		{[]string{"list", "--config", "C", "--insecure"}, "sard-agent repo list: flag provided but not defined: -insecure\n"},
		{[]string{"list", "--config", "C", "--timeout", "0s"}, "sard-agent repo list: --timeout must be a positive duration, got \"0s\"\n"},
		{[]string{"init", "--config", "C", "--timeout", "-3s", "main"}, "sard-agent repo init: --timeout must be a positive duration, got \"-3s\"\n"},
	}
	for _, c := range cases {
		h := newRepoHost(t)
		code, stdout, stderr := h.run(c.args...)
		if code != exitUsage || stdout != "" || stderr != c.want {
			t.Errorf("%v:\n code = %d, stdout = %q\n stderr = %q\n  want = %q", c.args, code, stdout, stderr, c.want)
		}
		h.assertNoBackendCalls()
	}
}

// A refused flag is reported once, on the command's own stderr: the flag
// package must not print its own copy (which would echo the flag) anywhere.
func TestABadFlagIsNotPrintedByTheFlagPackage(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	h := newRepoHost(t)
	h.run("init", "--config", "C", "--password", passMarker, "main")
	h.run("list", "--config", "C", "-h=false", "--nope")
	os.Stderr = old
	_ = w.Close()
	leaked, _ := io.ReadAll(r)
	_ = r.Close()
	if len(leaked) != 0 {
		t.Fatalf("the flag package printed %q", leaked)
	}
}

func TestAMissingConfigIsReportedOnceAndNothingElseRuns(t *testing.T) {
	for _, sub := range [][]string{{"init", "main"}, {"list"}} {
		h := newRepoHost(t)
		missing := h.path("nope.yaml")
		code, stdout, stderr := h.run(append([]string{sub[0], "--config", missing}, sub[1:]...)...)
		assertCode(t, code, exitUsage)
		if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], "sard-agent repo "+sub[0]+": reading config "+missing+": ") || stdout != "" {
			t.Errorf("%v: stdout = %q, stderr = %q", sub, stdout, stderr)
		}
		h.assertNoBackendCalls()
	}
}

// The smallest positive timeout is a timeout.
func TestTheSmallestPositiveTimeoutIsAccepted(t *testing.T) {
	h := newRepoHost(t)
	code, _, stderr := h.listCmd("--timeout", "1ns")
	if code != exitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestTheListTableIsAlignedInColumnsTwoSpacesApart(t *testing.T) {
	h := newRepoHost(t)
	h.main().initialized = true
	_, stdout, _ := h.listCmd()
	want := "NAME     BACKEND  STATUS           REPOSITORY_ID\n" +
		"main     local    initialized      " + h.main().id + "\n" +
		"offsite  rest     not-initialized  -\n"
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

// A name is text, whatever it looks like: markup in it counts as width.
func TestTheListTableTreatsNamesAsPlainText(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].Name = "<abcdefghij>"
	h.saveConfig()
	_, stdout, _ := h.listCmd()
	want := fmt.Sprintf("%-14s%-9s%-17s%s\n%-14s%-9s%-17s%s\n%-14s%-9s%-17s%s\n",
		"NAME", "BACKEND", "STATUS", "REPOSITORY_ID",
		"<abcdefghij>", "local", "not-initialized", "-",
		"offsite", "rest", "not-initialized", "-")
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

// Once the timeout has run out, later rows are marked without a call to restic.
func TestARowAfterTheTimeoutMakesNoCallToRestic(t *testing.T) {
	h := newRepoHost(t)
	h.main().hangCat = true
	done := h.start(context.Background(), "list", "--config", "C")
	for len(h.restic.callsTo(h.repoURL(), "cat")) == 0 {
		yield()
	}
	h.clock.fireNow()
	r := within(t, done)
	assertCode(t, r.code, exitTemporary)
	rows := listRows(t, r.stdout)
	if rows["main"][2] != "TIMEOUT" || rows["offsite"][2] != "TIMEOUT" || rows["offsite"][3] != "-" {
		t.Fatalf("rows = %v", rows)
	}
	if n := len(h.restic.callsTo(offsiteURL, "cat")); n != 0 {
		t.Fatalf("restic was called %d times for a row after the timeout", n)
	}
}

type failingReader struct {
	before func()
	err    error
}

func (f failingReader) Read([]byte) (int, error) {
	if f.before != nil {
		f.before()
	}
	return 0, f.err
}

func TestAnErrorThatIsNoFailureIsPrintedAsAnAgentError(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.pass()); err != nil {
		t.Fatal(err)
	}
	h.deps.random = failingReader{err: errors.New("no entropy")}
	code, stdout, stderr := h.initCmd("--generate-password")
	if code != exitAgentError || stdout != "" || stderr != "sard-agent repo init: generating a password: no entropy\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, err := os.Stat(h.pass()); err == nil {
		t.Fatal("a password file was created")
	}
}

// An interrupt outranks whatever error it caused.
func TestAnInterruptOutranksAnErrorThatIsNoFailure(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.pass()); err != nil {
		t.Fatal(err)
	}
	ctx, interrupt := context.WithCancel(context.Background())
	h.deps.random = failingReader{before: interrupt, err: errors.New("no entropy")}
	code, _, stderr := h.runCtx(ctx, "init", "--config", h.cfgPath, "--generate-password", "main")
	assertCode(t, code, exitTemporary)
	assertReason(t, stderr, "INTERRUPTED")
}
