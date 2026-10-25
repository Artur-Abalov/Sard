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

type lockAnswer struct{ err error }

func (l lockAnswer) CheckLock(context.Context) error { return l.err }

func TestStorageCredentialsThatCanWriteAreWarnedAboutAtStart(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))

	warnIfCanLock(t.Context(), "shared", lockAnswer{}, logger)
	warnIfCanLock(t.Context(), "readonly", lockAnswer{errors.New("unable to create lock in backend")}, logger)

	if !strings.Contains(log.String(), "repository=shared") || strings.Contains(log.String(), "readonly") {
		t.Fatalf("log %q", log.String())
	}
}
