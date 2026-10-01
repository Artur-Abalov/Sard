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
// *repoinit.Failure for a binary that cannot be used.
func checkRestic(ctx context.Context, cfg config.Config, configPath string, executable func() (string, error), exec restic.Executor) (string, error) {
	binary, err := resticPath(cfg.Restic.Path, executable)
	if err != nil {
		return "", err
	}
	cli := restic.New(restic.Options{Binary: binary, CacheDir: cfg.Restic.CacheDir, Exec: exec}, config.Repository{})
	err = cli.Check(ctx)
	var ce *restic.CheckError
	if errors.As(err, &ce) {
		return "", repoinit.FromCheck(ce, cfg.Restic.Path != "", configPath)
	}
	return binary, err
}
