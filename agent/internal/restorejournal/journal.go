// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package restorejournal is the durable journal of restores that plugins keep
// for a crash (ADR 0066): one file <command_id>.json in a directory of
// executor.state_dir, holding an envelope {plugin, command_id, entry}. The
// entry is the plugin's own bytes. The host writes the file whole or not at
// all, bounds its size and the number of entries of a plugin, and is the only
// one to remove it. It also reads, and never writes, the journal of the previous
// release (Options.Legacy).
package restorejournal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// Limits of the journal: the size of the entry of a plugin and the number of
// entries of one plugin.
const (
	MaxEntryBytes       = 1 << 20
	MaxEntriesPerPlugin = 64
)

const (
	suffix     = ".json"
	tempMarker = ".json.tmp-"
	dirMode    = 0o700
	fileMode   = 0o600
)

// Options configure a Store.
type Options struct {
	// Dir is the journal directory, <executor.state_dir>/restore-journal.
	Dir string
	FS  hostsetup.FS
	// Owner is the service user; the modes are the journal's own (0700, 0600).
	Owner hostsetup.Attrs
	// Legacy are the journals of the previous release, read for one release.
	Legacy []Legacy
}

// Legacy is a directory of the previous release: <command_id>.json files whose
// whole content is the entry of Plugin. The journal reads them as entries of
// that plugin and removes them with the entry of the command; it never writes
// there.
type Legacy struct {
	Dir    string
	Plugin string
}

// Store is the journal directory. It is safe for concurrent use.
type Store struct {
	opts Options

	mu sync.Mutex
}

// New returns the Store of the directory; the directory is made by the first Save.
func New(o Options) *Store { return &Store{opts: o} }

// envelope is the file of an entry.
type envelope struct {
	Plugin    string `json:"plugin"`
	CommandID string `json:"command_id"`
	Entry     []byte `json:"entry"`
}

// Record is an entry read from the journal.
type Record struct {
	Plugin    string
	CommandID string
	Entry     []byte
	Path      string
}

// Unreadable is a file of the journal that is no entry: it stays where it is.
type Unreadable struct {
	CommandID string
	Path      string
	Err       error
}

// Path is the file of the entry of a command.
func (s *Store) Path(commandID string) string {
	return filepath.Join(s.opts.Dir, commandID+suffix)
}

// usable: the command id is one component of a path, not a hidden file.
func usable(id string) bool {
	// Base("") is ".": "" is no name either; Base("/") is "/": the root is none.
	return filepath.Base(id) == id && id != string(filepath.Separator) && !strings.HasPrefix(id, ".")
}

// Save writes the entry of the command of the plugin, replacing the previous
// one whole or not at all: a temporary file, fsync, rename, fsync of the
// directory. A failure leaves the previous entry and no temporary file.
func (s *Store) Save(plugin, commandID string, entry []byte) error {
	if !usable(commandID) {
		return fmt.Errorf("command id %q cannot name a journal entry", commandID)
	}
	if len(entry) > MaxEntryBytes {
		return fmt.Errorf("the journal entry is %d bytes: the limit is %d bytes", len(entry), MaxEntryBytes)
	}
	data, _ := json.Marshal(envelope{Plugin: plugin, CommandID: commandID, Entry: append([]byte{}, entry...)}) // plain fields: Marshal cannot fail
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := hostsetup.EnsureDir(s.opts.FS, s.opts.Dir, s.attrs(dirMode)); err != nil {
		return err
	}
	if err := s.checkCount(plugin, commandID); err != nil {
		return err
	}
	return hostsetup.WriteFile(s.opts.FS, s.Path(commandID), data, s.attrs(fileMode))
}

func (s *Store) attrs(mode fs.FileMode) hostsetup.Attrs {
	a := s.opts.Owner
	a.Mode = mode
	return a
}

// checkCount refuses a new entry of a plugin that has the most it may have.
// Rewriting an entry that exists is always allowed.
func (s *Store) checkCount(plugin, commandID string) error {
	if _, err := s.opts.FS.Stat(s.Path(commandID)); err == nil {
		return nil
	}
	have, err := s.countOf(plugin)
	if err != nil {
		return err
	}
	if have >= MaxEntriesPerPlugin {
		return fmt.Errorf("the plugin has %d entries in the journal: the limit is %d entries", have, MaxEntriesPerPlugin)
	}
	return nil
}

// countOf is the number of entries the plugin has in the journal.
func (s *Store) countOf(plugin string) (int, error) {
	records, _, err := s.List()
	have := 0
	for _, r := range records {
		if r.Plugin == plugin {
			have++
		}
	}
	return have, err
}

// Remove deletes the entry of a command, in the journal and in the journals of
// the previous release; one that is not there is no error.
func (s *Store) Remove(commandID string) error {
	if !usable(commandID) {
		return fmt.Errorf("command id %q cannot name a journal entry", commandID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := []error{s.removeFrom(s.opts.Dir, commandID)}
	for _, l := range s.opts.Legacy {
		errs = append(errs, s.removeFrom(l.Dir, commandID), s.sweepTemporary(l.Dir, commandID))
	}
	return errors.Join(errs...)
}

// removeFrom deletes <dir>/<command_id>.json and makes that durable.
func (s *Store) removeFrom(dir, commandID string) error {
	removed, err := hostsetup.RemoveFile(s.opts.FS, filepath.Join(dir, commandID+suffix))
	if !removed { // RemoveFile says false with the error or without one
		return err
	}
	return s.opts.FS.SyncDir(dir)
}

// sweepTemporary deletes the temporary file the previous release wrote its entry to.
func (s *Store) sweepTemporary(dir, commandID string) error {
	_, err := hostsetup.RemoveFile(s.opts.FS, filepath.Join(dir, commandID+suffix+".tmp"))
	return err
}

// List reads the journal, entries ordered by command id, then the journals of
// the previous release: their files are entries of their plugin, and a command
// that is in the journal itself is taken from it only. A file that is no entry
// is reported in the second result and left alone; a temporary file a crash
// left is removed. A journal that was never made is empty. The error is that a
// directory cannot be read; what could be read is listed all the same.
func (s *Store) List() ([]Record, []Unreadable, error) {
	var found listing
	err := s.collectDir(&found)
	named := found.named()
	for _, l := range s.opts.Legacy {
		err = errors.Join(err, s.collectLegacy(l, named, &found))
	}
	slices.SortFunc(found.records, func(a, b Record) int { return strings.Compare(a.CommandID, b.CommandID) })
	slices.SortFunc(found.bad, func(a, b Unreadable) int { return strings.Compare(a.CommandID, b.CommandID) })
	return found.records, found.bad, err
}

// collectDir reads the journal directory into found.
func (s *Store) collectDir(found *listing) error {
	names, err := s.opts.FS.ReadDir(s.opts.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, n := range names {
		s.collect(n.Name(), found)
	}
	return nil
}

// collectLegacy reads a directory of the previous release into found, except the
// commands in named: those have their entry in the journal.
func (s *Store) collectLegacy(l Legacy, named map[string]bool, found *listing) error {
	names, err := s.opts.FS.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, n := range names {
		s.collectLegacyFile(l, n.Name(), named, found)
	}
	return nil
}

// collectLegacyFile judges one name of a directory of the previous release.
func (s *Store) collectLegacyFile(l Legacy, name string, named map[string]bool, found *listing) {
	id, isEntry := strings.CutSuffix(name, suffix)
	if !isEntry || !usable(id) || named[id] {
		return
	}
	path := filepath.Join(l.Dir, name)
	data, err := s.opts.FS.ReadFile(path)
	if err != nil {
		found.bad = append(found.bad, Unreadable{CommandID: id, Path: path, Err: err})
		return
	}
	found.records = append(found.records, Record{Plugin: l.Plugin, CommandID: id, Entry: data, Path: path})
}

// listing is what List found so far.
type listing struct {
	records []Record
	bad     []Unreadable
}

// named are the commands found so far, readable or not.
func (l *listing) named() map[string]bool {
	ids := map[string]bool{}
	for _, r := range l.records {
		ids[r.CommandID] = true
	}
	for _, u := range l.bad {
		ids[u.CommandID] = true
	}
	return ids
}

// collect judges one name of the directory.
func (s *Store) collect(name string, found *listing) {
	if isTemporary(name) {
		_ = s.opts.FS.Remove(filepath.Join(s.opts.Dir, name))
		return
	}
	id, isEntry := strings.CutSuffix(name, suffix)
	if !isEntry || strings.HasPrefix(name, ".") {
		return
	}
	rec, err := s.read(id)
	if err != nil {
		found.bad = append(found.bad, Unreadable{CommandID: id, Path: s.Path(id), Err: err})
		return
	}
	found.records = append(found.records, rec)
}

// isTemporary: the name of a temporary file of Save that a crash left.
func isTemporary(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, tempMarker)
}

// read parses the file of a command and judges its envelope.
func (s *Store) read(id string) (Record, error) {
	data, err := s.opts.FS.ReadFile(s.Path(id))
	if err != nil {
		return Record{}, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Record{}, fmt.Errorf("the journal entry is unreadable: %w", err)
	}
	switch {
	case env.Plugin == "":
		return Record{}, errors.New("the journal entry names no plugin")
	case env.CommandID != id:
		return Record{}, fmt.Errorf("the journal entry belongs to command %q, not to %q", env.CommandID, id)
	}
	return Record{Plugin: env.Plugin, CommandID: id, Entry: env.Entry, Path: s.Path(id)}, nil
}
