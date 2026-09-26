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

// Plugin backs up files and directories.
type Plugin struct{}

var _ sdk.Plugin = Plugin{}

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "files" }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

// Prepare will check that the paths exist (roadmap: first sources, stage 1).
func (Plugin) Prepare(context.Context, sdk.Config) error { return sdk.ErrNotImplemented }

// Dump will list the paths to back up (roadmap: first sources, stage 1).
func (Plugin) Dump(context.Context, sdk.Config) (sdk.Dump, error) {
	return sdk.Dump{}, sdk.ErrNotImplemented
}

// Stream will pipe the dump into restic (roadmap: first sources, stage 1).
func (Plugin) Stream(context.Context, sdk.Dump, io.Writer) error { return sdk.ErrNotImplemented }

// Verify will compare the restored tree with the snapshot listing (roadmap: restore verification, stage 2).
func (Plugin) Verify(context.Context, sdk.Config, string) error { return sdk.ErrNotImplemented }
