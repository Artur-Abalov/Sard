// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// registerKeysLimit is how long restic key list may take at Register, as long
// as the default --connect-timeout of the host commands (V3, K21).
const registerKeysLimit = 30 * time.Second

// recoveryKeyReader says, at every Register, whether each repository has a
// recovery key: restic key list --json --no-lock as the service user (K21).
// Its errors are scrubbed: they go to the log of the agent.
func recoveryKeyReader(repos resticRepositories, readFile func(string) ([]byte, error), limit time.Duration) func(ctx context.Context, r config.Repository) (agentv1.RecoveryKeyState, error) {
	return func(ctx context.Context, r config.Repository) (agentv1.RecoveryKeyState, error) {
		cli, ok := repos[r.Name]
		if !ok {
			return agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED, errors.New("the repository is not open")
		}
		return recoveryKeyState(ctx, cli.WithReadOnly(), scrubbingTarget(r, readFile), limit)
	}
}

// scrubbingTarget describes the repository to a message that goes to the log:
// the secrets of its address, env file and password file are hidden.
func scrubbingTarget(r config.Repository, readFile func(string) ([]byte, error)) repoinit.Target {
	target := repoconnect.TargetReading(r, readFile)
	if data, err := readFile(r.PasswordFile); err == nil {
		target = withSecret(withSecret(target, string(data)), strings.TrimSpace(string(data)))
	}
	return target
}

// recoveryKeyState reads the keys within limit and decides.
func recoveryKeyState(ctx context.Context, cli *restic.CLI, target repoinit.Target, limit time.Duration) (agentv1.RecoveryKeyState, error) {
	unknown := agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED
	limited, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	keys, err := cli.Keys(limited)
	switch {
	case err != nil && ctx.Err() == nil && limited.Err() != nil:
		return unknown, fmt.Errorf("restic key list did not answer within %s", limit)
	case err != nil:
		return unknown, errors.New(repoinit.FromRestic(ctx, err, target).Detail)
	case restickeys.HasRecovery(keys):
		return agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_PRESENT, nil
	}
	return agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT, nil
}
