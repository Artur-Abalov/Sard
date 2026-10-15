// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

const (
	markerName = "marker"
	markerTemp = "marker.tmp"
)

// marker says what a restore is about to put in place (ВК13): written
// durably before the rename, it tells a start after a kill that the
// destination is complete.
type marker struct {
	Destination string `json:"destination"`
	Files       uint64 `json:"files_restored"`
	Bytes       uint64 `json:"bytes_restored"`
	// OwnersRestored is always false: the service is unprivileged (ВВ3).
	OwnersRestored bool `json:"owners_restored"`
}

// writeMarker writes the marker into the held <command_id> so that it is
// there whole or not at all: a temporary file, fsync, rename, fsync of the
// directory.
func writeMarker(dir hostsetup.Dir, m marker) (err error) {
	data, _ := json.Marshal(m) // plain fields: Marshal cannot fail
	defer func() {
		if err != nil {
			_ = dir.Remove(markerTemp)
		}
	}()
	f, err := dir.CreateFile(markerTemp, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if err := errors.Join(werr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := dir.Rename(markerTemp, markerName); err != nil {
		return err
	}
	return dir.Sync()
}

// readMarker reads the marker of the held <command_id>; found is false if
// there is none.
func readMarker(dir hostsetup.Dir) (m marker, found bool, err error) {
	f, err := dir.OpenFile(markerName)
	if errors.Is(err, fs.ErrNotExist) {
		return marker{}, false, nil
	}
	if err != nil {
		return marker{}, false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return marker{}, false, err
	}
	m, err = parseMarker(data)
	return m, err == nil, err
}

// parseMarker reads the marker and judges its destination by the rule of a plan.
func parseMarker(data []byte) (m marker, err error) {
	if err := json.Unmarshal(data, &m); err != nil {
		return marker{}, fmt.Errorf("the marker is unreadable: %w", err)
	}
	if err := (sdk.RestorePlan{Destination: m.Destination}).Validate(); err != nil {
		return marker{}, fmt.Errorf("the marker names an invalid destination: %w", err)
	}
	return m, nil
}

// removeCommand removes the directory of a command that did not complete.
// Removing a tree is unordered, and a kill after root and before the marker
// would leave a marker without root: the sign of a completed restore. So the
// marker goes first and the directory is synced; if the marker stays,
// root stays too, and the next start judges the entry again. A directory that
// cannot be synced does not stop the cleanup: the order then holds on the
// in-order metadata commit of a journaling filesystem (one that does not
// journal is not covered), and the failure is logged.
func removeCommand(staging hostsetup.Dir, name string, log *slog.Logger) error {
	if dir, err := staging.Open(name); err == nil {
		err = removeMarkers(dir, log)
		_ = dir.Close()
		if err != nil {
			return err
		}
	}
	return hostsetup.RemoveTree(staging, name)
}

// removeMarkers removes the marker and its temporary file from the directory
// of a command and syncs the directory.
func removeMarkers(dir hostsetup.Dir, log *slog.Logger) error {
	for _, n := range []string{markerName, markerTemp} {
		if err := dir.Remove(n); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := dir.Sync(); err != nil {
		log.Warn("the removal of the marker could not be synced to disk; the order of marker and root rests on the filesystem journal",
			"path", dir.Path(), "error", err)
	}
	return nil
}
