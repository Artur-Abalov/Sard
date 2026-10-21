// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// hangingExec is a restic that never answers until it is stopped.
type hangingExec struct{}

func (hangingExec) Run(ctx context.Context, _ restic.Command) (int, error) {
	<-ctx.Done()
	return -1, nil
}

func keysCLI(exec restic.Executor) *restic.CLI {
	repo := config.Repository{Name: "base", URL: "/srv/base", PasswordFile: "/etc/sard/base.pass"}
	return restic.New(restic.Options{Binary: "/opt/restic", Exec: exec, Keys: crypto.NewResticAES(map[string]string{"base": repo.PasswordFile})}, repo)
}

// K21: restic key list that does not answer within the limit leaves the flag unknown.
func TestAResticThatDoesNotAnswerWithinTheLimitLeavesTheRecoveryKeyUnknown(t *testing.T) {
	t.Parallel()
	start := time.Now()
	state, err := recoveryKeyState(context.Background(), keysCLI(hangingExec{}), repoinit.Named("base"), 50*time.Millisecond)
	if state != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED || err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("state %v, err %v", state, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the limit did not stop restic")
	}
}

func TestAnInterruptIsNotTakenForTheLimit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := recoveryKeyState(ctx, keysCLI(hangingExec{}), repoinit.Named("base"), time.Hour)
	if err == nil || strings.Contains(err.Error(), "within") {
		t.Fatalf("err %v", err)
	}
}
