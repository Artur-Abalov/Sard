// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package files is the files and directories source plugin.
package files

import (
	"context"
	_ "embed"
	"io"
	"io/fs"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

//go:embed restore_schema.json
var restoreSchema []byte

// Plugin backs up files and directories: Prepare checks that the listed
// paths can be read, restic then stores them (see docs/plugins/files.md).
// It restores from the console into a new directory of the host
// (sdk.Restorer, restore.go). Each backup carries a fingerprint of the
// source: the control paths, a sample of hashed files and restic's totals
// (fingerprint.go), which a restore check compares with the copy.
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
	// FS is the file system the plugin checks the paths on; nil is the
	// host's.
	FS FS
	// Tree is the file system the fingerprint reads, rooted at root: "/"
	// for the source, the directory of a copy; nil is os.DirFS.
	Tree func(root string) fs.FS
	// Rand picks a number in [0, n) for the sample; nil is math/rand/v2.
	Rand func(n int) int
	// Now is the clock of the time limit of the walk; nil is time.Now.
	Now func() time.Time
	// WalkLimit bounds the walk of the sample, 10 minutes when zero;
	// ReadLimit bounds the bytes it hashes, 1 GiB when zero.
	WalkLimit time.Duration
	ReadLimit int64
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
// normalisation, nested paths and control paths outside them: those are a
// *sdk.ConfigError. Then every listed path must exist and be readable (the
// tree below it is not walked), and every control path must exist and not be
// empty.
func (p Plugin) Prepare(ctx context.Context, _ sdk.Host, cfg sdk.Config) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	return p.checkPaths(ctx, c.Paths, c.ControlPaths)
}

// Dump implements sdk.Plugin: restic backs up the normalised paths. The draft
// of the fingerprint is taken now, before restic reads the files.
func (p Plugin) Dump(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	c, err := parse(cfg)
	if err != nil {
		return sdk.Dump{}, err
	}
	draft, err := p.Fingerprint(ctx, h, cfg, sdk.FingerprintTarget{})
	if err != nil {
		return sdk.Dump{}, err
	}
	return sdk.Dump{Paths: cleaned(c.Paths), Excludes: c.Exclude, OneFileSystem: c.OneFileSystem, Fingerprint: &draft}, nil
}

// Stream implements sdk.Plugin: the plugin dumps paths, never a stream.
func (Plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return sdk.ErrNotImplemented
}
