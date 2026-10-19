// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
)

// newRenewal builds the renewal of the agent's certificate. It calls the
// server over connections of their own (transport.CertClient).
func newRenewal(cfg config.Config, hostname string, rt agentRuntime) *certrenew.Manager {
	return certrenew.New(certrenew.Options{
		Files:     cfg.TLS,
		StatePath: filepath.Join(executorStateDir(cfg.Executor.StateDir), certrenew.StateFile),
		FS:        rt.fs,
		Caller:    transport.NewCertClient(cfg.Server.Address, cfg.TLS.CAFile),
		Clock:     rt.clock,
		Rand:      rt.rand,
		Random:    rt.random,
		Hostname:  hostname,
		Logger:    rt.logger,
		// Until the server confirms the renewed pair the agent waits as it does between connections.
		ReconnectDelay: func(attempt int) time.Duration { return transport.Backoff(attempt, rt.rand) },
	})
}

// adoptPending confirms a renewed pair that start-up found next to the
// current one and renames it into place, before Register (П21). stop is true
// when the agent was stopped meanwhile. A refusal of the server for good
// removes the new pair and the agent goes on with the current one, if that is
// still usable.
func adoptPending(ctx context.Context, renewal *certrenew.Manager, local localState, configPath string, rt agentRuntime) (stop bool, err error) {
	if !local.pair.Pending() {
		return false, nil
	}
	err = renewal.Adopt(ctx, local.pair)
	switch {
	case err == nil:
		return false, nil
	case ctx.Err() != nil:
		return true, nil
	case errors.Is(err, certrenew.ErrDiscarded):
		return false, usableAfterDiscard(local.cfg, configPath, rt)
	}
	return true, err
}

// usableAfterDiscard: the current pair must still be one the agent can start with.
func usableAfterDiscard(cfg config.Config, configPath string, rt agentRuntime) error {
	sel, err := certpair.Select(cfg.TLS, rt.fs, rt.clock.Now())
	if err != nil {
		return err
	}
	if sel.Expired != nil {
		return expiredRefusal(sel.Expired, configPath, cfg)
	}
	return nil
}

// runRenewing runs the agent and, beside it, the renewal of its certificate,
// which depends on neither the stream nor the steps (П30).
func runRenewing(ctx context.Context, renewal *certrenew.Manager, run func(context.Context) error) error {
	renewalCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		renewal.Run(renewalCtx)
		close(done)
	}()
	err := run(ctx)
	stop()
	<-done
	return err
}
