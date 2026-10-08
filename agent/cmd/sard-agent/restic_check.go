// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// checkRestic finds the restic binary and makes sure it is usable and not
// older than the minimum (С6, С7): the path, or the error — a
// *refusal.Failure for a binary that cannot be used.
func checkRestic(ctx context.Context, cfg config.Config, configPath string, executable func() (string, error), exec restic.Executor, as *restic.RunAs) (string, error) {
	binary, err := resticPath(cfg.Restic.Path, executable)
	if err != nil {
		return "", err
	}
	cli := restic.New(restic.Options{Binary: binary, CacheDir: cfg.Restic.CacheDir, Exec: exec, RunAs: as}, config.Repository{})
	err = cli.Check(ctx)
	var ce *restic.CheckError
	if errors.As(err, &ce) {
		return "", repoinit.FromCheck(ce, cfg.Restic.Path != "", configPath)
	}
	return binary, err
}

// checkCryptoProviders refuses to start while a repository asks for
// encryption the agent cannot do; the first one in config order is named.
// It is a check of the service's start, not of config.Load: repo init and
// repo list report the same reason themselves.
func checkCryptoProviders(cfg config.Config) error {
	for _, r := range cfg.Repositories {
		if f := repoinit.CheckCryptoProvider(r); f != nil {
			return f
		}
	}
	return nil
}
