// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

type fakeClient struct {
	registerErr, connectErr error
	got                     *agentv1.RegisterRequest
	connected               bool
}

func (f *fakeClient) Register(_ context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	f.got = req
	return &agentv1.RegisterResponse{AgentId: "a1"}, f.registerErr
}

func (f *fakeClient) Connect(context.Context) (agentv1.AgentService_ConnectClient, error) {
	f.connected = true
	return nil, f.connectErr
}

type plugin struct{ name, schema string }

func (p plugin) Name() string                                     { return p.name }
func (p plugin) ConfigSchema() []byte                             { return []byte(p.schema) }
func (plugin) Prepare(context.Context, sdk.Config) error          { return nil }
func (plugin) Dump(context.Context, sdk.Config) (sdk.Dump, error) { return sdk.Dump{}, nil }
func (plugin) Stream(context.Context, sdk.Dump, io.Writer) error  { return nil }
func (plugin) Verify(context.Context, sdk.Config, string) error   { return nil }

func newAgent(t *testing.T, c *fakeClient) *app.Agent {
	t.Helper()
	reg, err := sdk.NewRegistry(plugin{"mysql", `{"a":1}`}, plugin{"files", `{"b":2}`})
	if err != nil {
		t.Fatal(err)
	}
	return &app.Agent{Client: c, Plugins: reg, Hostname: "db1", Version: "1.2.3"}
}

func TestRunRegistersWithHostAndVersionThenConnects(t *testing.T) {
	c := &fakeClient{}
	if err := newAgent(t, c).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.got.GetHostname() != "db1" || c.got.GetAgentVersion() != "1.2.3" {
		t.Errorf("request = %v", c.got)
	}
	if !c.connected {
		t.Error("agent did not open the command stream")
	}
}

func TestRunAnnouncesPluginsWithSchemasSortedByName(t *testing.T) {
	c := &fakeClient{}
	if err := newAgent(t, c).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range c.got.GetPlugins() {
		got = append(got, p.GetName()+"="+p.GetConfigSchema())
	}
	if strings.Join(got, " ") != `files={"b":2} mysql={"a":1}` {
		t.Errorf("plugins = %v", got)
	}
}

func TestRunStopsWhenRegisterFails(t *testing.T) {
	c := &fakeClient{registerErr: sdk.ErrNotImplemented}
	err := newAgent(t, c).Run(context.Background())
	if !errors.Is(err, sdk.ErrNotImplemented) || err.Error() != "register: not implemented" {
		t.Fatalf("err = %v", err)
	}
	if c.connected {
		t.Error("connected after a failed registration")
	}
}

func TestRunReportsConnectFailure(t *testing.T) {
	err := newAgent(t, &fakeClient{connectErr: errors.New("refused")}).Run(context.Background())
	if err == nil || err.Error() != "connect: refused" {
		t.Fatalf("err = %v", err)
	}
}
