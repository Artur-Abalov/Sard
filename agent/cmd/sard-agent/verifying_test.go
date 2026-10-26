// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
)

func verifyConfig(t *testing.T) config.Config {
	t.Helper()
	work := filepath.Join(t.TempDir(), "verify")
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nverify:\n  work_dir: " + work + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestASourceHasNoVerifier(t *testing.T) {
	v := newVerifying(verifyConfig(t), hostIdentity{role: role.Source}, verifier.OSDisk{}, slog.New(slog.DiscardHandler))
	agent := &app.Agent{}
	v.attach(agent)
	v.warnIfWritable(t.Context(), nil, nil)
	if v != nil || v.options() != nil || agent.Verifier != nil || agent.VerifierChanged != nil {
		t.Fatalf("verifying = %+v, agent = %+v", v, agent)
	}
}

// V5, test 7: what a killed verification left goes before any step.
func TestAVerifierRemovesWhatEarlierVerificationsLeftAndReportsItsStatus(t *testing.T) {
	cfg := verifyConfig(t)
	work := cfg.VerifySettings().WorkDir
	if err := os.MkdirAll(filepath.Join(work, "job-1", "copy"), 0o700); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer

	v := newVerifying(cfg, hostIdentity{role: role.Verifier, machineID: strings.Repeat("1", 64)}, verifier.OSDisk{}, slog.New(slog.NewTextHandler(&log, nil)))

	if entries, err := os.ReadDir(work); err != nil || len(entries) != 0 {
		t.Fatalf("left %v, %v", entries, err)
	}
	if !strings.Contains(log.String(), "removed what verifications that did not end left") {
		t.Errorf("log %q", log.String())
	}
	requireAttached(t, v)
}

func requireAttached(t *testing.T, v *verifying) {
	t.Helper()
	agent := &app.Agent{}
	v.attach(agent)
	if len(v.options()) != 1 || agent.Verifier == nil || agent.VerifierChanged == nil {
		t.Fatalf("verifying = %+v", v)
	}
	if st := agent.Verifier(t.Context()); st.WorkFreeBytes == nil {
		t.Errorf("status = %v", st)
	}
}

func TestAWorkDirectoryThatCannotBeSweptIsLoggedAsAnError(t *testing.T) {
	var log bytes.Buffer
	disk := &stuckDisk{}

	_ = newVerifying(verifyConfig(t), hostIdentity{role: role.Verifier}, disk, slog.New(slog.NewTextHandler(&log, nil)))

	if !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), "busy") {
		t.Fatalf("log %q", log.String())
	}
}

type stuckDisk struct{ verifier.OSDisk }

func (*stuckDisk) Entries(string) ([]string, error) { return []string{"job-1"}, nil }

func (*stuckDisk) RemoveAll(string) error { return errors.New("device or resource busy") }

type lockAnswer struct {
	err error
	// left is how long the question had until its deadline.
	left *time.Duration
}

func (l lockAnswer) CheckLock(ctx context.Context) error {
	if l.left != nil {
		deadline, _ := ctx.Deadline()
		*l.left = time.Until(deadline)
	}
	return l.err
}

func TestStorageCredentialsThatCanWriteAreWarnedAboutAtStart(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))

	warnIfCanLock(t.Context(), "shared", lockAnswer{}, logger)
	warnIfCanLock(t.Context(), "readonly", lockAnswer{err: errors.New("unable to create lock in backend")}, logger)

	if !strings.Contains(log.String(), "repository=shared") || strings.Contains(log.String(), "readonly") {
		t.Fatalf("log %q", log.String())
	}
}

// V5, found on the stand: restic 0.19.1 takes a lock even for `cat config`,
// which storage that only reads refuses; a verifier reads without one.
func TestAVerifierReadsItsRepositoriesWithoutLockOrCache(t *testing.T) {
	script, calls := recordingRestic(t)
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nrepositories:\n  - {name: main, url: /srv/main, password_file: /etc/sard/main.pass}\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, readOnly := range []bool{true, false} {
		agent := buildAgent(cfg, "v1", script, "", nil, nil, readOnly)
		if id, err := agent.RepositoryID(t.Context(), cfg.Repositories[0]); err != nil || id != "REPO-A" {
			t.Fatalf("id %q, %v", id, err)
		}
	}
	lines := calls()
	if len(lines) != 2 || !strings.Contains(lines[0], "--no-lock --no-cache") || strings.Contains(lines[1], "--no-lock") {
		t.Fatalf("restic was run as %q", lines)
	}
}

func TestOnlyAVerifierReadsOnly(t *testing.T) {
	if !(&verifying{}).readsOnly() || (*verifying)(nil).readsOnly() {
		t.Error("readsOnly")
	}
}

// recordingRestic is a restic that writes its arguments to a file and prints
// the config of repository REPO-A; calls reads what it was run with.
func recordingRestic(t *testing.T) (string, func() []string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "restic")
	body := "#!/bin/sh\necho \"$@\" >> " + file + "\necho '{\"id\":\"REPO-A\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, func() []string {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

func TestTheQuestionOfTheLockHasTwoMinutes(t *testing.T) {
	var left time.Duration
	warnIfCanLock(t.Context(), "r", lockAnswer{err: errors.New("no"), left: &left}, slog.New(slog.DiscardHandler))

	if left <= 2*time.Minute-10*time.Second || left > 2*time.Minute {
		t.Fatalf("left %v", left)
	}
}

func TestAVerifierAsksEveryRepositoryAndASourceNone(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	repos := map[string]locker{"a": lockAnswer{}, "b": lockAnswer{}}

	(*verifying)(nil).warnIfWritable(t.Context(), repos, logger)
	if log.Len() != 0 {
		t.Fatalf("a source asked: %q", log.String())
	}
	(&verifying{}).warnIfWritable(t.Context(), repos, logger)
	if !strings.Contains(log.String(), "repository=a") || !strings.Contains(log.String(), "repository=b") {
		t.Fatalf("log %q", log.String())
	}
}

func TestAVerifierRefreshesItsRuntimeEveryTenMinutes(t *testing.T) {
	v := newVerifying(verifyConfig(t), hostIdentity{role: role.Verifier}, verifier.OSDisk{}, slog.New(slog.DiscardHandler))
	if v.status.Refresh != 10*time.Minute {
		t.Fatalf("refresh %v", v.status.Refresh)
	}
}

func TestAnEmptyWorkDirectoryIsNotReportedAsSwept(t *testing.T) {
	var log bytes.Buffer
	_ = newVerifying(verifyConfig(t), hostIdentity{role: role.Verifier}, verifier.OSDisk{}, slog.New(slog.NewTextHandler(&log, nil)))
	if strings.Contains(log.String(), "removed") {
		t.Fatalf("log %q", log.String())
	}
}

func TestAWorkDirectoryThatCannotBeMadeIsLogged(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nverify:\n  work_dir: " + file + "/verify\n"))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	_ = newVerifying(cfg, hostIdentity{role: role.Verifier}, verifier.OSDisk{}, slog.New(slog.NewTextHandler(&log, nil)))
	if !strings.Contains(log.String(), "cannot make the work directory of verifications") {
		t.Fatalf("log %q", log.String())
	}
}

func TestLockersAreTheRepositoriesByName(t *testing.T) {
	cfg, _ := config.Parse([]byte("server:\n  address: s:1\nrepositories:\n  - {name: main, url: /srv/main, password_file: /p}\n"))
	got := lockers(openRepositories(cfg, resticOptions(cfg, "restic")))
	if _, ok := got["main"]; !ok || len(got) != 1 {
		t.Fatalf("lockers %v", got)
	}
}
