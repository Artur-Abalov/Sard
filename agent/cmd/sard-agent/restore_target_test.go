// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

func TestTheRestoreTargetOfARepositoryOfTheHostIsDescribedAndAnUnknownOneIsItsNameOnly(t *testing.T) {
	cfg := config.Config{Repositories: []config.Repository{{Name: "main", URL: "/srv/repo"}}}
	targets := restoreTargets(cfg, func(string) ([]byte, error) { return nil, nil })
	if got := targets("main"); got.Name != "main" || got.Backend != "local" {
		t.Errorf("main = %+v", got)
	}
	if got := targets("ghost"); got.Name != "ghost" || got.Scrub("x") != "x" {
		t.Errorf("ghost = %+v", got)
	}
}
