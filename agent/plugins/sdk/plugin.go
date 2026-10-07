// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

// Package sdk defines the contract every Sard source plugin implements.
//
// A backup of one source runs Prepare → Dump → Stream; a restore check runs
// Verify against a restored copy. The agent validates the configuration
// against ConfigSchema before Prepare, so a plugin sees only valid Config.
// The package is Apache-2.0 and imports no AGPL code, so third-party
// plugins can depend on it freely.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Config is a plugin configuration document, valid against ConfigSchema.
// It names secrets but never holds their values (ADR 0008).
type Config = json.RawMessage

// SecretFormat marks a string field of ConfigSchema that holds the name of
// a secret defined on the agent host: {"type": "string", "format":
// "sard-secret"}. The agent rejects a config naming an unknown secret
// before Prepare runs; the plugin reads the value with Host.Secret.
const SecretFormat = "sard-secret"

// Dump is what Plugin.Dump produced, in one of two forms:
//
//   - by paths: restic backs up Paths (minus Excludes) from the local file
//     system; Stream is not called;
//   - as a stream: Filename is set, Paths is empty, and the agent calls
//     Stream, whose bytes restic stores as a single file named Filename.
type Dump struct {
	// Paths are local files or directories that make up the snapshot.
	Paths []string
	// Excludes are restic --exclude patterns applied to Paths.
	Excludes []string
	// OneFileSystem keeps restic on the file systems of Paths: it does not
	// descend into other mounts. Only for a dump by paths: set together
	// with Filename it fails the step.
	OneFileSystem bool
	// Filename is the file name of a streamed dump inside the snapshot,
	// e.g. "db.sql". Setting it selects streaming.
	Filename string
	// Tags are snapshot tags of the plugin: the agent stores them in restic
	// as "<plugin name>.<key>=<value>" after the step's own tags, e.g.
	// "postgresql.server_version=16.4". A key that makes a tag the step
	// already has fails the step before restic runs. Keys and values must
	// not contain commas.
	Tags map[string]string
	// Extra are small files stored after the main snapshot, each as a
	// snapshot of its own (restic stores one stdin file per snapshot).
	Extra []ExtraFile
}

// ExtraFile is a small file a plugin stores in addition to its main dump,
// e.g. the global objects of a database cluster. The agent saves it only
// after the main snapshot succeeded, as a snapshot with the step's tags, the
// file's Tags and "<plugin name>.main_snapshot=<id of the main snapshot>".
// The content is held in memory: keep it small.
type ExtraFile struct {
	// Name is the file name inside the snapshot.
	Name string
	// Content is the file's bytes.
	Content []byte
	// Tags are added to the snapshot like Dump.Tags, with the same prefix.
	Tags map[string]string
}

// Streamed reports whether the dump is written by Stream.
func (d Dump) Streamed() bool { return d.Filename != "" }

// ErrNotImplemented is returned by plugin methods that are not written yet.
var ErrNotImplemented = errors.New("not implemented")

// Level is the severity of a plugin log line.
type Level int

// Log levels.
const (
	LevelDebug Level = iota + 1
	LevelInfo
	LevelWarn
	LevelError
)

// Host is what the agent offers a plugin while a step runs. It is safe for
// concurrent use.
type Host interface {
	// Secret returns the value of the named secret, read from its file on
	// the agent host. Within one step it is the value the step started with,
	// the one the agent masks in the step's log. An unknown name is a
	// *SecretError. The value must never be logged.
	Secret(name string) ([]byte, error)
	// Progress reports the progress of the current step; total is 0 when
	// unknown.
	Progress(done, total uint64)
	// Log sends a line to the step's log on the server. The agent masks the
	// step's secrets in text, each call on its own: write multiline text in
	// one call, a secret split between calls is not found.
	Log(level Level, text string)
}

// Plugin is a backup source: a database, a directory tree, a device config.
type Plugin interface {
	// Name is the unique plugin identifier, e.g. "postgresql".
	Name() string
	// Version is the plugin version the server shows, e.g. "1.2.0".
	Version() string
	// ConfigSchema returns the JSON Schema (draft 2020-12) of Config.
	// The UI renders configuration forms from it.
	ConfigSchema() []byte
	// Prepare runs pre-flight checks and pre-backup hooks. A config the
	// schema cannot express as invalid is reported as a *ConfigError, and
	// only before any side effect: the agent then rejects the step.
	Prepare(ctx context.Context, h Host, cfg Config) error
	// Dump makes a consistent snapshot of the source.
	Dump(ctx context.Context, h Host, cfg Config) (Dump, error)
	// Stream writes a streamed dump to w (restic's stdin); it is called
	// only when d.Streamed(), with the d that Dump returned. It must return
	// when ctx is done: the agent then stops restic without storing a
	// snapshot. A returned error also discards the snapshot.
	Stream(ctx context.Context, h Host, cfg Config, d Dump, w io.Writer) error
}

// Verifier is implemented by plugins that can check a restored copy.
// Only they are offered the verify action.
type Verifier interface {
	// Verify checks a restored copy located at restoredPath.
	Verify(ctx context.Context, h Host, cfg Config, restoredPath string) error
}
