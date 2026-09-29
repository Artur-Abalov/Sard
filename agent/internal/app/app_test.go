// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// fakeLink stands in for the transport.
type fakeLink struct {
	err       error
	connected bool
}

func (f *fakeLink) Run(context.Context) error {
	f.connected = true
	return f.err
}

type plugin struct{ name, schema string }

// handlers offers a handler with the given actions per plugin name.
type handlers map[string][]agentv1.Action

func (h handlers) Handler(name string) (executor.Handler, bool) {
	actions, ok := h[name]
	return actionsOnly(actions), ok
}

type actionsOnly []agentv1.Action

func (a actionsOnly) Actions() []agentv1.Action { return a }
func (actionsOnly) Run(context.Context, *agentv1.RunStep, executor.Reporter) (*agentv1.StepResult, error) {
	return nil, nil
}

func (p plugin) Name() string                                                          { return p.name }
func (plugin) Version() string                                                         { return "0.9.0" }
func (p plugin) ConfigSchema() []byte                                                  { return []byte(p.schema) }
func (plugin) Prepare(context.Context, sdk.Host, sdk.Config) error                     { return nil }
func (plugin) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error)            { return sdk.Dump{}, nil }
func (plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error { return nil }

func newAgent(t *testing.T, link *fakeLink) *app.Agent {
	t.Helper()
	reg, err := sdk.NewRegistry(plugin{"mysql", `{"a":1}`}, plugin{"files", `{"b":2}`})
	if err != nil {
		t.Fatal(err)
	}
	local := config.Config{
		Repositories: []config.Repository{
			{Name: "main", URL: "s3:https://s3.example.com/b", PasswordFile: "/etc/sard/main.pass"},
			{Name: "nas", URL: "/mnt/nas", PasswordFile: "/etc/sard/nas.pass", CryptoProvider: "restic-aes"},
		},
		Secrets: map[string]string{"pg-prod": "/etc/sard/pg", "ssh": "/etc/sard/ssh"},
		Scripts: map[string]string{"freeze": "/usr/local/bin/freeze"},
	}
	// Only "main" can be opened; "nas" reports no id yet.
	repoID := func(_ context.Context, r config.Repository) (string, error) {
		if r.Name == "main" {
			return "5e3f0a9c", nil
		}
		return "", sdk.ErrNotImplemented
	}
	return &app.Agent{
		Link: link, Plugins: reg, Handlers: handlers{"files": {agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE}},
		Hostname: "db1", Version: "1.2.3", OS: "linux", Arch: "amd64",
		Local: local, RepositoryID: repoID,
	}
}

func registered(t *testing.T) *agentv1.RegisterRequest {
	t.Helper()
	return newAgent(t, &fakeLink{}).RegisterRequest(context.Background())
}

func TestRegisterRequestCarriesHostAndVersion(t *testing.T) {
	req := registered(t)
	got := []any{req.GetHostname(), req.GetAgentVersion(), req.GetProtocolVersion(), req.GetOs(), req.GetArch()}
	want := []any{"db1", "1.2.3", uint32(1), "linux", "amd64"}
	if !slices.Equal(got, want) {
		t.Errorf("request = %v, want %v", got, want)
	}
}

func TestRegisterRequestAnnouncesPluginsWithSchemasSortedByName(t *testing.T) {
	var got []string
	for _, p := range registered(t).GetPlugins() {
		got = append(got, fmt.Sprintf("%s@%s=%s %v", p.GetName(), p.GetVersion(), p.GetConfigSchema(), p.GetActions()))
	}
	// Each plugin has its own version and the actions of its handler; a
	// plugin without a handler offers none.
	want := `files@0.9.0={"b":2} [ACTION_BACKUP ACTION_RESTORE]|mysql@0.9.0={"a":1} []`
	if strings.Join(got, "|") != want {
		t.Errorf("plugins = %v", got)
	}
}

// Register carries names and metadata of host-local definitions (ADR 0008).
func TestRegisterRequestAnnouncesHostInventory(t *testing.T) {
	req := registered(t)
	var repos []string
	for _, r := range req.GetRepositories() {
		repos = append(repos, r.GetName()+"/"+r.GetBackend()+"/"+r.GetRepositoryId()+"/"+r.GetCryptoProvider())
	}
	got := []string{strings.Join(repos, " "), strings.Join(req.GetSecretNames(), " "), strings.Join(req.GetScriptNames(), " ")}
	want := []string{"main/s3/5e3f0a9c/ nas/local//restic-aes", "pg-prod ssh", "freeze"}
	if !slices.Equal(got, want) {
		t.Errorf("inventory = %q, want %q", got, want)
	}
}

// Never URLs, password files, secret files or script paths.
func TestRegisterRequestNeverSendsHostLocalValues(t *testing.T) {
	wire := registered(t).String()
	for _, value := range []string{"/etc/sard", "s3.example.com", "/mnt/nas", "/usr/local"} {
		if strings.Contains(wire, value) {
			t.Errorf("register request leaks %q: %s", value, wire)
		}
	}
}

// Run is the transport's loop; its error comes back unchanged.
func TestRunConnectsThroughTheLink(t *testing.T) {
	link := &fakeLink{err: errors.New("stream: EOF")}
	if err := newAgent(t, link).Run(context.Background()); err != link.err || !link.connected {
		t.Fatalf("err = %v, connected = %v", err, link.connected)
	}
}

// Until the executor (A4) is wired in, nothing runs and commands are dropped.
