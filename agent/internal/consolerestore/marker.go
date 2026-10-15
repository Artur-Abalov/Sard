// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
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
	if err := json.Unmarshal(data, &m); err != nil {
		return marker{}, false, fmt.Errorf("the marker is unreadable: %w", err)
	}
	return m, true, nil
}
