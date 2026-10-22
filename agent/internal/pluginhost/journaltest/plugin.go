// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package journaltest is a plugin for tests only; the agent never registers
// it. It restores and recovers itself, as a third-party plugin would, and does
// what the test tells it: reports phases, reads snapshots and the stream of a
// snapshot, writes its journal entry, returns a result or an error. Through it
// the tests drive the host's side of RestoreRunner and Recoverer.
package journaltest

import (
	"context"
	"io"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

const schema = `{"type": "object", "properties": {"field": {"type": "string"}}, "additionalProperties": false}`

// Plugin is the test plugin; its name is PluginName, "journaltest" if empty.
type Plugin struct {
	PluginName string
	// Plan answers PlanRestore; nil: a plan without a destination.
	Plan func(cfg sdk.Config) (sdk.RestorePlan, error)
	// RestoreFn is the restore; nil: it restores nothing and succeeds.
	RestoreFn func(ctx context.Context, h sdk.RestoreHost, cfg sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error)
	// RecoverFn is the recovery; nil: nothing was restored.
	RecoverFn func(ctx context.Context, h sdk.RecoveryHost, commandID string, entry []byte) sdk.Recovery
}

var (
	_ sdk.Plugin        = (*Plugin)(nil)
	_ sdk.Restorer      = (*Plugin)(nil)
	_ sdk.RestoreRunner = (*Plugin)(nil)
	_ sdk.Recoverer     = (*Plugin)(nil)
)

// Name implements sdk.Plugin.
func (p *Plugin) Name() string {
	if p.PluginName == "" {
		return "journaltest"
	}
	return p.PluginName
}

// Version implements sdk.Plugin.
func (*Plugin) Version() string { return "0.0.0-test" }

// ConfigSchema implements sdk.Plugin.
func (*Plugin) ConfigSchema() []byte { return []byte(schema) }

// Prepare implements sdk.Plugin; the plugin does not back up.
func (*Plugin) Prepare(context.Context, sdk.Host, sdk.Config) error { return sdk.ErrNotImplemented }

// Dump implements sdk.Plugin; the plugin does not back up.
func (*Plugin) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
	return sdk.Dump{}, sdk.ErrNotImplemented
}

// Stream implements sdk.Plugin; the plugin does not back up.
func (*Plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return sdk.ErrNotImplemented
}

// RestoreConfigSchema implements sdk.Restorer.
func (*Plugin) RestoreConfigSchema() []byte { return []byte(schema) }

// PlanRestore implements sdk.Restorer.
func (p *Plugin) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	if p.Plan == nil {
		return sdk.RestorePlan{}, nil
	}
	return p.Plan(cfg)
}

// Restore implements sdk.RestoreRunner.
func (p *Plugin) Restore(ctx context.Context, h sdk.RestoreHost, cfg sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
	if p.RestoreFn == nil {
		return sdk.RestoreResult{}, nil
	}
	return p.RestoreFn(ctx, h, cfg, req)
}

// Recover implements sdk.Recoverer.
func (p *Plugin) Recover(ctx context.Context, h sdk.RecoveryHost, commandID string, entry []byte) sdk.Recovery {
	if p.RecoverFn == nil {
		return sdk.NothingRestored()
	}
	return p.RecoverFn(ctx, h, commandID, entry)
}
