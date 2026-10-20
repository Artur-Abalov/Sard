// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package files is the files and directories source plugin.
package files

import (
	"context"
	_ "embed"
	"io"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

//go:embed restore_schema.json
var restoreSchema []byte

// Plugin backs up files and directories: Prepare checks that the listed
// paths can be read, restic then stores them (see docs/plugins/files.md).
// It restores from the console into a new directory of the host
// (sdk.Restorer, restore.go). Restore verification will compare the restored
// tree with the snapshot listing (roadmap: restore verification, stage 2).
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
	// FS is the file system the plugin checks the paths on; nil is the
	// host's.
	FS FS
}

var (
	_ sdk.Plugin   = Plugin{}
	_ sdk.Restorer = Plugin{}
)

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "files" }

// Version implements sdk.Plugin: the plugin ships with the agent.
func (p Plugin) Version() string { return p.AgentVersion }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

// Prepare implements sdk.Plugin. The schema cannot tell repeats after
// normalisation and nested paths: those are a *sdk.ConfigError. Then every
// listed path must exist and be readable; the tree below it is not walked.
func (p Plugin) Prepare(ctx context.Context, _ sdk.Host, cfg sdk.Config) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	return p.checkPaths(ctx, c.Paths)
}

// Dump implements sdk.Plugin: restic backs up the normalised paths.
func (Plugin) Dump(_ context.Context, _ sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	c, err := parse(cfg)
	if err != nil {
		return sdk.Dump{}, err
	}
	return sdk.Dump{Paths: cleaned(c.Paths), Excludes: c.Exclude, OneFileSystem: c.OneFileSystem}, nil
}

// Stream implements sdk.Plugin: the plugin dumps paths, never a stream.
func (Plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return sdk.ErrNotImplemented
}
