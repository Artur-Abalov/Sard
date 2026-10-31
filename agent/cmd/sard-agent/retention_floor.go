// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"log/slog"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// logRetentionFloors tells the operator of the host, at start, the floor the
// agent holds for every repository and where it comes from (КL3); a floor
// set for a repository the host does not have is a warning, not an error.
func logRetentionFloors(cfg config.Config, log *slog.Logger) {
	for _, r := range cfg.Repositories {
		log.Info(floorLine(r.Name, cfg.FloorOf(r.Name)))
	}
	for _, name := range cfg.UnknownFloorRepositories() {
		log.Warn(fmt.Sprintf("retention_floor.repositories.%s names a repository that this host does not have", name))
	}
}

func floorLine(repository string, f config.Floor) string {
	if f.Disabled {
		return fmt.Sprintf("retention floor repository=%s disabled origin=%s", repository, f.Origin)
	}
	return fmt.Sprintf("retention floor repository=%s keep_last=%d keep_within_days=%d origin=%s", repository, f.KeepLast, f.KeepWithinDays, f.Origin)
}
