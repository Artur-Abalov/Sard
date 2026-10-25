// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"errors"
	"io"
	"path"
	"slices"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// errNoSecret: a verifier reaches no production system and holds no secret of one.
var errNoSecret = errors.New("a verifier has no secrets of a source")

// copyHost is the sdk.RestoreHost a plugin restores and measures a copy with:
// the repository of the step, read without a lock, and the directory of the job.
type copyHost struct{ x *run }

var _ sdk.RestoreHost = copyHost{}

func (x *run) host() copyHost { return copyHost{x} }

func (copyHost) Secret(string) ([]byte, error) { return nil, errNoSecret }

func (copyHost) Progress(uint64, uint64) {}

func (copyHost) Phase(sdk.Phase) {}

// levels maps SDK log levels to the protocol's; others are INFO.
var levels = map[sdk.Level]agentv1.LogLevel{
	sdk.LevelDebug: agentv1.LogLevel_LOG_LEVEL_DEBUG,
	sdk.LevelWarn:  agentv1.LogLevel_LOG_LEVEL_WARN,
	sdk.LevelError: agentv1.LogLevel_LOG_LEVEL_ERROR,
}

func (h copyHost) Log(level sdk.Level, text string) {
	l, ok := levels[level]
	if !ok {
		l = agentv1.LogLevel_LOG_LEVEL_INFO
	}
	h.x.r.Log(l, text)
}

// Journal: a copy lives in the directory of the job, which the verifier
// removes whatever happens; there is nothing to recover.
func (copyHost) Journal() sdk.Journal { return noJournal{} }

func (h copyHost) Snapshots(ctx context.Context) ([]sdk.SnapshotInfo, error) {
	listed, err := h.x.store.Snapshots(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]sdk.SnapshotInfo, len(listed))
	for i, s := range listed {
		out[i] = sdk.SnapshotInfo{ID: s.ID, Time: s.Time, Tags: slices.Clone(s.Tags), Paths: slices.Clone(s.Paths)}
	}
	return out, nil
}

func (h copyHost) Dump(ctx context.Context, snapshotID, file string, w io.Writer) error {
	return h.x.store.Dump(ctx, snapshotID, file, w)
}

// RestoreTree restores the snapshot into <job directory>/copy, reading every
// file back (restic restore --verify).
func (h copyHost) RestoreTree(ctx context.Context, snapshotID string) (string, error) {
	target := path.Join(h.x.dir, "copy")
	if err := h.x.store.RestoreVerified(ctx, snapshotID, target); err != nil {
		return "", err
	}
	return target, nil
}

type noJournal struct{}

func (noJournal) Save([]byte) error { return nil }
