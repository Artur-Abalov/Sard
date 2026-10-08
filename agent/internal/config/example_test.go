// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// The example is copied as-is to /etc/sard/agent.yaml by the console's install
// steps (FXc): it must load and name no repository, secret or script, because
// the files they point to do not exist on a fresh host.
func TestAgentExampleDefinesNoFilesOfItsOwn(t *testing.T) {
	cfg, err := config.Load("../../../deploy/agent/agent.example.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Repositories) != 0 || len(cfg.SecretNames()) != 0 || len(cfg.ScriptNames()) != 0 {
		t.Errorf("repositories %v, secrets %v, scripts %v; want none", cfg.Repositories, cfg.SecretNames(), cfg.ScriptNames())
	}
}
