// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// P2 is the value of the secret pg-restore of the host.
const secretP2 = "hunter2-P2"

// safeBuffer is the log of the agent, written by several goroutines.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// faultyFS is the file system of the journal; it fails what the test asks.
type faultyFS struct {
	hostsetup.FS
	writeErr, syncErr, renameErr, mkdirErr error
}

func (f *faultyFS) CreateTemp(dir, pattern string) (hostsetup.File, error) {
	file, err := f.FS.CreateTemp(dir, pattern)
	return &faultyFile{File: file, fs: f}, err
}

func (f *faultyFS) Rename(oldpath, newpath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	return f.FS.Rename(oldpath, newpath)
}

func (f *faultyFS) OpenRootDir() (hostsetup.Dir, error) {
	d, err := f.FS.OpenRootDir()
	return faultyDir{Dir: d, fs: f}, err
}

type faultyFile struct {
	hostsetup.File
	fs *faultyFS
}

func (f *faultyFile) Write(p []byte) (int, error) {
	if f.fs.writeErr != nil {
		return 0, f.fs.writeErr
	}
	return f.File.Write(p)
}

func (f *faultyFile) Sync() error {
	if f.fs.syncErr != nil {
		return f.fs.syncErr
	}
	return f.File.Sync()
}

type faultyDir struct {
	hostsetup.Dir
	fs *faultyFS
}

func (d faultyDir) Open(name string) (hostsetup.Dir, error) {
	c, err := d.Dir.Open(name)
	return faultyDir{Dir: c, fs: d.fs}, err
}

func (d faultyDir) Mkdir(name string, perm fs.FileMode) (hostsetup.Dir, error) {
	if d.fs.mkdirErr != nil {
		return nil, &fs.PathError{Op: "mkdirat", Path: name, Err: d.fs.mkdirErr}
	}
	c, err := d.Dir.Mkdir(name, perm)
	return faultyDir{Dir: c, fs: d.fs}, err
}

// ownRig is a host with the plugins given, as the agent builds it: a restore
// directory, a journal in the state directory, the repository main (a fake).
type ownRig struct {
	t        *testing.T
	repo     *repo
	handlers *pluginhost.Handlers
	reg      *sdk.Registry
	secrets  *pluginhost.Secrets
	// state is executor.state_dir, dir is D_R, the restore directory.
	state, dir string
	journalFS  *faultyFS
	store      *restorejournal.Store
	journal    *pluginhost.Journal
	plugins    []sdk.Plugin
	logs       *safeBuffer
	// repositories are the names the handlers looked up.
	mu           sync.Mutex
	repositories []string
}

func (r *ownRig) looked(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.repositories = append(r.repositories, name)
}

func (r *ownRig) lookedUp() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.repositories)
}

func newOwnRig(t *testing.T, plugins ...sdk.Plugin) *ownRig {
	t.Helper()
	// The state directory is the executor's: 0700, as the executor requires.
	r := &ownRig{t: t, plugins: plugins, repo: &repo{}, state: filepath.Join(t.TempDir(), "state"), logs: &safeBuffer{}}
	if err := os.Mkdir(r.state, 0o700); err != nil {
		t.Fatal(err)
	}
	r.dir = filepath.Join(t.TempDir(), "restore")
	r.repo.snapshots = []restic.Snapshot{{ID: fullID, Time: time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), Tags: []string{"sard.source=s1", "x.part=main"}}}
	r.journalFS = &faultyFS{FS: hostsetup.OS{}}
	r.store = restorejournal.New(restorejournal.Options{Dir: r.journalDir(), FS: r.journalFS, Owner: r.attrs()})
	r.secrets = pluginhost.NewSecrets(map[string]string{"pg-restore": "/etc/sard/pg-restore", "gone": "/etc/sard/gone"},
		(&files{data: map[string]string{"/etc/sard/pg-restore": secretP2}}).read)
	r.rebuild(plugins...)
	return r
}

func (r *ownRig) attrs() hostsetup.Attrs {
	return hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}
}

// rebuild makes the registry, the journal of the host and the handlers anew over
// the same disk and the same log: what a restart of the agent does. With the
// plugins given, those replace the plugins of the rig.
func (r *ownRig) rebuild(plugins ...sdk.Plugin) {
	r.t.Helper()
	if len(plugins) > 0 {
		r.plugins = plugins
	}
	reg, err := sdk.NewRegistry(r.plugins...)
	if err != nil {
		r.t.Fatal(err)
	}
	r.reg = reg
	logger := slog.New(slog.NewTextHandler(r.logs, nil))
	engine := consolerestore.New(consolerestore.Options{Dir: r.dir, FS: hostsetup.OS{}, Owner: r.attrs(), Logger: slog.New(slog.DiscardHandler)})
	r.journal = pluginhost.NewJournal(r.store, logger)
	repos := func(name, _ string, _ io.Writer) (restic.Repository, bool) {
		r.looked(name)
		return r.repo, name == "main"
	}
	r.handlers, err = pluginhost.NewHandlers(reg, r.secrets, repos, filepath.Join(r.t.TempDir(), "verify"),
		pluginhost.WithConsoleRestore(engine), pluginhost.WithRestoreJournal(r.journal), pluginhost.WithLogger(logger))
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *ownRig) journalDir() string { return filepath.Join(r.state, "restore-journal") }

func (r *ownRig) entryPath(id string) string { return filepath.Join(r.journalDir(), id+".json") }

// step is the step T: command c1, plugin journaltest, the snapshot F.
func (r *ownRig) step() *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", Plugin: "journaltest", Action: restore, RepositoryName: "main", SnapshotId: fullID, ConfigJson: "{}"}
}

// run runs the step of the plugin named by the step the way the executor does.
func (r *ownRig) run(step *agentv1.RunStep) (*agentv1.StepResult, error) {
	res, _, err := r.runReported(step)
	return res, err
}

func (r *ownRig) runReported(step *agentv1.RunStep) (*agentv1.StepResult, *reporter, error) {
	r.t.Helper()
	h, ok := r.handlers.Handler(step.GetPlugin())
	if !ok {
		r.t.Fatalf("no handler for %q", step.GetPlugin())
	}
	rep := &reporter{snapshot: map[string][]byte{"pg-restore": []byte(secretP2)}}
	res, err := h.Run(context.Background(), step, rep)
	return res, rep, err
}

// envelopeOf reads the file of the entry of a command.
func (r *ownRig) envelopeOf(id string) (plugin, commandID string, entry []byte, found bool) {
	r.t.Helper()
	records, _, err := r.store.List()
	if err != nil {
		r.t.Fatal(err)
	}
	for _, rec := range records {
		if rec.CommandID == id {
			return rec.Plugin, rec.CommandID, rec.Entry, true
		}
	}
	return "", "", nil, false
}

func (r *ownRig) hasEntry(id string) bool {
	_, _, _, found := r.envelopeOf(id)
	return found
}

func (r *ownRig) entryOf(id string) string {
	r.t.Helper()
	_, _, entry, found := r.envelopeOf(id)
	if !found {
		r.t.Fatalf("no entry of %s", id)
	}
	return string(entry)
}

// restoresOneFile plays restic restoring a snapshot of one file, /srv/a.
func restoresOneFile(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
	if err := os.MkdirAll(filepath.Join(restoreRoot(req), "srv"), 0o700); err != nil {
		return restic.RestoreSummary{}, err
	}
	return restic.RestoreSummary{Files: 2, Bytes: 1536}, os.WriteFile(filepath.Join(restoreRoot(req), "srv/a"), []byte("A"), 0o600)
}
