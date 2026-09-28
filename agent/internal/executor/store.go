// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// State directory layout (all owner-only: results may name paths and repositories):
//
//	<dir>/results/<key>.json  finished, waiting for Ack   {"version":1,"result":{…}}
//	<dir>/acked/<key>.json    acknowledged (tombstone)    {"version":1,"acked_at":"…","result":{…}}
//
// <key> is hex(SHA-256(command_id)): the id comes from the server and may hold
// "/" or "..". Every file is written to a temporary name, fsynced and renamed.
const (
	resultsDir    = "results"
	ackedDir      = "acked"
	recordVersion = 1
	tempPrefix    = ".tmp-"
	stateFileMode = 0o600
	stateDirMode  = 0o700
)

type record struct {
	Version int             `json:"version"`
	AckedAt *time.Time      `json:"acked_at,omitempty"`
	Result  json.RawMessage `json:"result"`
}

// stored is a command found on disk at start.
type stored struct {
	result  *agentv1.StepResult
	ackedAt time.Time // zero while not acknowledged
}

// store keeps results until the server acknowledges them.
type store struct {
	dir string
}

// openStore creates the directories owner-only and refuses a state dir others can reach.
func openStore(dir string) (*store, error) {
	for _, sub := range []string{"", resultsDir, ackedDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), stateDirMode); err != nil {
			return nil, fmt.Errorf("%w: state dir: %w", ErrInvalidOptions, err)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: state dir: %w", ErrInvalidOptions, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: state dir %s is accessible beyond its owner (%v)", ErrInvalidOptions, dir, info.Mode().Perm())
	}
	return &store{dir: dir}, nil
}

func key(commandID string) string {
	sum := sha256.Sum256([]byte(commandID))
	return hex.EncodeToString(sum[:]) + ".json"
}

func (s *store) path(sub, commandID string) string {
	return filepath.Join(s.dir, sub, key(commandID))
}

// saveResult records a finished command.
func (s *store) saveResult(r *agentv1.StepResult) error {
	return s.write(resultsDir, r, nil)
}

// acknowledge writes the tombstone first, then drops the result: a crash in
// between leaves both, and loading prefers the tombstone.
func (s *store) acknowledge(r *agentv1.StepResult, at time.Time) error {
	if err := s.write(ackedDir, r, &at); err != nil {
		return err
	}
	return s.remove(resultsDir, r.GetCommandId())
}

func (s *store) forget(commandID string) error {
	return s.remove(ackedDir, commandID)
}

func (s *store) remove(sub, commandID string) error {
	if err := os.Remove(s.path(sub, commandID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *store) write(sub string, r *agentv1.StepResult, ackedAt *time.Time) error {
	result, err := protojson.Marshal(r)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record{Version: recordVersion, AckedAt: ackedAt, Result: result})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, sub), key(r.GetCommandId()), data)
}

// writeAtomic: temporary file (0600) → write → fsync → rename → fsync the directory.
func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // fails harmlessly once renamed
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Sync(), tmp.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// load reads both directories. Partial writes are removed; unreadable files are
// kept for inspection and reported through bad.
func (s *store) load(bad func(path string, err error)) (map[string]stored, error) {
	found := map[string]stored{}
	for _, sub := range []string{resultsDir, ackedDir} { // acked last: it wins
		if err := s.loadDir(sub, found, bad); err != nil {
			return nil, err
		}
	}
	return found, nil
}

func (s *store) loadDir(sub string, found map[string]stored, bad func(string, error)) error {
	dir := filepath.Join(s.dir, sub)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if strings.HasPrefix(entry.Name(), tempPrefix) {
			_ = os.Remove(path)
			continue
		}
		st, err := readRecord(path)
		if err != nil {
			bad(path, err)
			continue
		}
		found[st.result.GetCommandId()] = st
	}
	return nil
}

var errUnknownVersion = errors.New("unknown state file version")

func readRecord(path string) (stored, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return stored{}, err
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return stored{}, err
	}
	if rec.Version != recordVersion {
		return stored{}, fmt.Errorf("%w %d", errUnknownVersion, rec.Version)
	}
	r := &agentv1.StepResult{}
	if err := protojson.Unmarshal(rec.Result, r); err != nil {
		return stored{}, err
	}
	st := stored{result: r}
	if rec.AckedAt != nil {
		st.ackedAt = *rec.AckedAt
	}
	return st, nil
}
