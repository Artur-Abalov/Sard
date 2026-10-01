// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The rules of sard-server's Register for plugins (S4a), copied from
// server/src/main/kotlin/dev/sard/server/registration/SnapshotRules.kt;
// the e2e job checks the same snapshot against the real server (T2a).
var (
	serverName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`) // NAME
	serverLabel = regexp.MustCompile(`^[!-~]{1,64}$`)                      // LABEL
)

const (
	serverMaxPlugins      = 64        // MAX_PLUGINS
	serverMaxSchemaBytes  = 64 * 1024 // MAX_CONFIG_SCHEMA_BYTES
	agentVersionInRelease = "0.1.0"
)

// Strategy 6: the built-in plugins, as the agent announces them, pass the
// server's snapshot rules for plugins.
func TestBuiltinPluginsPassTheServersRegisterRules(t *testing.T) {
	for _, version := range []string{"dev", agentVersionInRelease} {
		reg := plugins.Registry(version)
		handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil), func(string) (restic.Repository, bool) { return nil, false }, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		a := &app.Agent{Plugins: reg, Handlers: handlers, Hostname: "db1", Version: version, OS: "linux", Arch: "amd64",
			RepositoryID: func(context.Context, config.Repository) (string, error) { return "", nil }}
		checkPlugins(t, a.RegisterRequest(context.Background()).GetPlugins())
	}
}

func checkPlugins(t *testing.T, ps []*agentv1.Plugin) {
	t.Helper()
	if len(ps) == 0 || len(ps) > serverMaxPlugins {
		t.Fatalf("%d plugins", len(ps))
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if !serverName.MatchString(p.GetName()) || seen[p.GetName()] {
			t.Errorf("name %q: NAME_INVALID or NAME_DUPLICATE", p.GetName())
		}
		seen[p.GetName()] = true
		checkPlugin(t, p)
	}
}

func checkPlugin(t *testing.T, p *agentv1.Plugin) {
	t.Helper()
	if !serverLabel.MatchString(p.GetVersion()) {
		t.Errorf("%s: version %q: FIELD_INVALID", p.GetName(), p.GetVersion())
	}
	schema := p.GetConfigSchema()
	if len(schema) > serverMaxSchemaBytes || !json.Valid([]byte(schema)) || strings.Contains(schema, `\u0000`) {
		t.Errorf("%s: CONFIG_SCHEMA_INVALID or SNAPSHOT_TOO_LARGE", p.GetName())
	}
	actions := p.GetActions()
	unique := slices.Compact(slices.Sorted(slices.Values(actions)))
	if len(actions) == 0 || slices.Contains(actions, agentv1.Action_ACTION_UNSPECIFIED) || len(unique) != len(actions) {
		t.Errorf("%s: actions %v: FIELD_INVALID", p.GetName(), actions)
	}
}

// A6b: Register announces backup and restore for files, with the version of
// the agent, and a schema that is ready for the console's form.
func TestRegisterAnnouncesTheFilesPlugin(t *testing.T) {
	reg := plugins.Registry(agentVersionInRelease)
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(nil, nil), func(string) (restic.Repository, bool) { return nil, false }, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app.Agent{Plugins: reg, Handlers: handlers, Hostname: "db1", Version: agentVersionInRelease, OS: "linux", Arch: "amd64",
		RepositoryID: func(context.Context, config.Repository) (string, error) { return "", nil }}
	for _, p := range a.RegisterRequest(context.Background()).GetPlugins() {
		if p.GetName() != "files" {
			continue
		}
		wantActions := []agentv1.Action{agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE}
		if !slices.Equal(p.GetActions(), wantActions) || p.GetVersion() != agentVersionInRelease {
			t.Errorf("files: actions %v, version %q", p.GetActions(), p.GetVersion())
		}
		return
	}
	t.Fatal("no files plugin in Register")
}
