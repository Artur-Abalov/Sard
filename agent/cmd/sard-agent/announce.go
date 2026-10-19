// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"log/slog"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// recordAnnounced is the recorder of app.Agent.Announced: at every Register
// the repositories and ids told to the server are written to the state dir
// (Ч4), for sard-agent repo check. A record that cannot be written is logged
// and nothing else: the Register goes on.
func recordAnnounced(stateDir string, logger *slog.Logger) func([]*agentv1.RepositoryInfo) {
	return func(infos []*agentv1.RepositoryInfo) {
		repos := make([]hostcheck.AnnouncedRepository, len(infos))
		for i, r := range infos {
			repos[i] = hostcheck.AnnouncedRepository{Name: r.GetName(), RepositoryID: r.GetRepositoryId()}
		}
		if err := hostcheck.WriteAnnounced(stateDir, repos); err != nil {
			logger.Warn("the announced repositories were not recorded", "error", err)
		}
	}
}
