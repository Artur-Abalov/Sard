// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
)

// StateFile is the name of the renewal state in executor.state_dir (П25).
const StateFile = "cert-renewal.json"

// stateMode keeps the file to its owner; it holds no key material, only the
// last error.
const stateMode fs.FileMode = 0o600

// State is what `sard-agent status` shows of a renewal that keeps failing.
type State struct {
	// Serial is the certificate being renewed, lowercase hex.
	Serial      string    `json:"serial"`
	Failures    int       `json:"failures"`
	LastError   string    `json:"last_error"`
	LastAttempt time.Time `json:"last_attempt"`
	NextAttempt time.Time `json:"next_attempt"`
}

// WriteState replaces the state file atomically.
func WriteState(fsys certpair.FS, path string, st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return fsys.WriteFile(path, data, stateMode)
}

// RemoveState deletes the state file; a missing file is not an error.
func RemoveState(fsys certpair.FS, path string) error {
	if err := fsys.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ReadState reads the state file. A missing, unreadable or broken file, or one
// without a serial, is as if there was none.
func ReadState(read func(string) ([]byte, error), path string) (State, bool) {
	data, err := read(path)
	if err != nil {
		return State{}, false
	}
	var st State
	if json.Unmarshal(data, &st) != nil || st.Serial == "" {
		return State{}, false
	}
	return st, true
}
