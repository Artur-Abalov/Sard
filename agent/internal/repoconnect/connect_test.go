// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// fakeRepo is the repository behind restic: it opens with one password.
type fakeRepo struct {
	restic.Repository
	initialized bool
	password    string
	id          string
	initErr     error
	// files are the files the repository was opened with, in order.
	files []repoconnect.Files
	// passwordSeen is the content of the password file at each call.
	passwordSeen []string
	inits        int
	current      *repoconnect.Files
}

func (r *fakeRepo) ID(context.Context) (string, error) {
	data, _ := os.ReadFile(r.current.Password)
	got := strings.TrimSpace(string(data))
	r.passwordSeen = append(r.passwordSeen, got)
	switch {
	case !r.initialized:
		return "", fmt.Errorf("restic cat: %w", restic.ErrNoRepository)
	case got != r.password:
		return "", fmt.Errorf("restic cat: %w", restic.ErrWrongPassword)
	}
	return r.id, nil
}

func (r *fakeRepo) Init(context.Context) (string, error) {
	r.inits++
	if r.initErr != nil {
		return "", r.initErr
	}
	data, _ := os.ReadFile(r.current.Password)
	r.initialized, r.password = true, strings.TrimSpace(string(data))
	return r.id, nil
}

// failingFS fails the rename of a file whose name contains failRename.
type failingFS struct {
	hostsetup.OS
	failRename string
}

func (f failingFS) Rename(oldpath, newpath string) error {
	if f.failRename != "" && strings.Contains(newpath, f.failRename) {
		return errors.New("injected")
	}
	return f.OS.Rename(oldpath, newpath)
}

type world struct {
	t    *testing.T
	dir  string
	repo *fakeRepo
	conn *repoconnect.Connector
	asks int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, dir: dir, repo: &fakeRepo{id: "ID-1"}}
	owner := hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()}
	w.conn = &repoconnect.Connector{
		FS:         hostsetup.OS{},
		Random:     strings.NewReader(strings.Repeat("x", 64)),
		SecretsDir: filepath.Join(dir, "secrets"),
		Final:      filepath.Join(dir, "secrets", "restic-extra.pass"),
		Owner:      func(mode os.FileMode) hostsetup.Attrs { a := owner; a.Mode = mode; return a },
		Open: func(files repoconnect.Files, _ io.Writer) (repoinit.Target, restic.Repository) {
			w.repo.files = append(w.repo.files, files)
			w.repo.current = &files
			return repoinit.Target{Name: "extra", Backend: "s3", Scrub: func(s string) string { return s }}, w.repo
		},
		AskPassword: func() ([]byte, *refusal.Failure) {
			w.asks++
			return []byte("asked\n"), nil
		},
		State: &repoconnect.State{},
	}
	return w
}

func (w *world) env() string { return filepath.Join(w.dir, "secrets", "restic-extra.env") }

func (w *world) content(path string) string {
	w.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data)
}

func (w *world) leftovers() []string {
	entries, _ := os.ReadDir(filepath.Join(w.dir, "secrets"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAnEmptyStorageGetsARepositoryWithAGeneratedPassword(t *testing.T) {
	w := newWorld(t)
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	if w.repo.inits != 1 || w.conn.ID != "ID-1" || w.conn.Attached || w.conn.Generated == "" {
		t.Fatalf("inits %d, state %+v", w.repo.inits, w.conn.State)
	}
	if got := w.content(w.conn.Final); got != w.conn.Generated+"\n" {
		t.Fatalf("password file %q", got)
	}
	w.assertMode(w.conn.Final, 0o600)
	w.assertInitRanWithTheFinalFile()
}

func (w *world) assertInitRanWithTheFinalFile() {
	w.t.Helper()
	if got := w.repo.files[len(w.repo.files)-1]; got.Password != w.conn.Final {
		w.t.Fatalf("init ran with %+v", got)
	}
	if names := w.leftovers(); len(names) != 1 {
		w.t.Fatalf("files %v", names)
	}
}

func (w *world) assertMode(path string, mode os.FileMode) {
	w.t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != mode {
		w.t.Fatalf("%s: %v, %v", path, info, err)
	}
}

func TestAnExistingRepositoryIsAttachedWithTheGivenPassword(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "given"
	w.conn.Provided = []byte("given\n")
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	if w.repo.inits != 0 || !w.conn.Attached || w.conn.ID != "ID-1" || w.asks != 0 {
		t.Fatalf("inits %d asks %d state %+v", w.repo.inits, w.asks, w.conn.State)
	}
	if got := w.content(w.conn.Final); got != "given\n" {
		t.Fatalf("password file %q", got)
	}
}

func TestAWrongGivenPasswordIsFinalAndLeavesNothing(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "another"
	w.conn.Provided = []byte("given\n")
	f := w.conn.Connect(t.Context())
	if f == nil || f.Reason != refusal.WrongPassword || w.asks != 0 {
		t.Fatalf("failure %v, asks %d", f, w.asks)
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
}

func TestAGeneratedPasswordThatDoesNotOpenAnExistingRepositoryAsksTheOperator(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	if w.asks != 1 || !w.conn.Attached || string(w.conn.Provided) != "asked\n" {
		t.Fatalf("asks %d state %+v", w.asks, w.conn.State)
	}
	if got := w.content(w.conn.Final); got != "asked\n" {
		t.Fatalf("password file %q", got)
	}
	if names := w.leftovers(); len(names) != 1 {
		t.Fatalf("files %v", names)
	}
}

func TestTheLeftoverPasswordFileIsUsedAsItIs(t *testing.T) {
	w := newWorld(t)
	ok(t, os.MkdirAll(w.conn.SecretsDir, 0o700))
	ok(t, os.WriteFile(w.conn.Final, []byte("Q\n"), 0o600))
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	if w.repo.password != "Q" || w.conn.Generated != "" || w.content(w.conn.Final) != "Q\n" {
		t.Fatalf("repository %+v state %+v", w.repo, w.conn.State)
	}
}

func TestAFailureOfInitKeepsThePasswordFileForARepeat(t *testing.T) {
	w := newWorld(t)
	w.repo.initErr = fmt.Errorf("restic init: %w", &restic.ExitError{Code: 1, Message: "Fatal: boom"})
	f := w.conn.Connect(t.Context())
	if f == nil || f.Reason != refusal.BackendRefused {
		t.Fatalf("failure %v", f)
	}
	if _, err := os.Stat(w.conn.Final); err != nil {
		t.Fatal(err)
	}
}

func TestTheEnvFileOfAnS3RepositoryIsStagedTriedAndCommittedWithThePassword(t *testing.T) {
	w := newWorld(t)
	w.conn.Env = &repoconnect.EnvFile{Final: w.env(), Content: []byte("AWS_ACCESS_KEY_ID=K\n")}
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	first := w.repo.files[0]
	if first.Env == w.env() || !strings.Contains(first.Env, ".tmp-") {
		t.Fatalf("the first access used %q, not a staged file", first.Env)
	}
	if last := w.repo.files[len(w.repo.files)-1]; last.Env != w.env() || last.Password != w.conn.Final {
		t.Fatalf("init ran with %+v", last)
	}
	if got := w.content(w.env()); got != "AWS_ACCESS_KEY_ID=K\n" {
		t.Fatalf("env file %q", got)
	}
	w.assertMode(w.env(), 0o600)
	if len(w.leftovers()) != 2 {
		t.Fatalf("files %v", w.leftovers())
	}
}

func TestWithoutNewKeysTheEnvFileInPlaceIsTheCandidate(t *testing.T) {
	w := newWorld(t)
	ok(t, os.MkdirAll(w.conn.SecretsDir, 0o700))
	ok(t, os.WriteFile(w.env(), []byte("AWS_ACCESS_KEY_ID=K\n"), 0o600))
	w.conn.Env = &repoconnect.EnvFile{Final: w.env()}
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	for _, files := range w.repo.files {
		if files.Env != w.env() {
			t.Fatalf("restic ran with the env file %q", files.Env)
		}
	}
	if got := w.content(w.env()); got != "AWS_ACCESS_KEY_ID=K\n" {
		t.Fatalf("env file %q", got)
	}
}

func TestAFailedCommitOfTheEnvFileLeavesNoPasswordFileAndNoTemporaryFile(t *testing.T) {
	w := newWorld(t)
	w.conn.FS = failingFS{failRename: "restic-extra.env"}
	w.conn.Env = &repoconnect.EnvFile{Final: w.env(), Content: []byte("AWS_ACCESS_KEY_ID=K\n")}
	f := w.conn.Connect(t.Context())
	if f == nil || f.Reason != refusal.ConfigWrite || !strings.Contains(f.Detail, "restic-extra.env") {
		t.Fatalf("failure %v", f)
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
	if w.repo.inits != 0 {
		t.Fatal("init ran")
	}
}

func TestARejectedEnvFileCandidateIsRemoved(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "another"
	w.conn.Provided = []byte("given\n")
	w.conn.Env = &repoconnect.EnvFile{Final: w.env(), Content: []byte("AWS_ACCESS_KEY_ID=K\n")}
	if f := w.conn.Connect(t.Context()); f == nil {
		t.Fatal("no failure")
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// failingCreate fails the temporary files whose name contains match.
type failingCreate struct {
	hostsetup.OS
	match string
}

func (f failingCreate) CreateTemp(dir, pattern string) (hostsetup.File, error) {
	if strings.Contains(pattern, f.match) {
		return nil, errors.New("injected")
	}
	return f.OS.CreateTemp(dir, pattern)
}

func TestATemporaryFileThatCannotBeMadeIsAWriteErrorThatNamesTheFile(t *testing.T) {
	for match, name := range map[string]string{"restic-extra.env": "restic-extra.env", "restic-extra.pass": "restic-extra.pass"} {
		w := newWorld(t)
		w.conn.FS = failingCreate{match: match}
		w.conn.Env = &repoconnect.EnvFile{Final: w.env(), Content: []byte("AWS_ACCESS_KEY_ID=K\n")}
		f := w.conn.Connect(t.Context())
		if f == nil || f.Reason != refusal.ConfigWrite || !strings.Contains(f.Detail, name) {
			t.Errorf("%s: failure %v", match, f)
		}
		if names := w.leftovers(); len(names) != 0 {
			t.Errorf("%s: files %v", match, names)
		}
	}
}

func TestASecretsDirectoryThatCannotBeMadeIsAWriteError(t *testing.T) {
	for _, withEnv := range []bool{false, true} {
		w := newWorld(t)
		ok(t, os.WriteFile(filepath.Join(w.dir, "file"), nil, 0o600))
		w.conn.SecretsDir = filepath.Join(w.dir, "file", "secrets")
		if withEnv {
			w.conn.Env = &repoconnect.EnvFile{Final: w.env(), Content: []byte("AWS_ACCESS_KEY_ID=K\n")}
		}
		if f := w.conn.Connect(t.Context()); f == nil || f.Reason != refusal.ConfigWrite {
			t.Errorf("env %v: failure %v", withEnv, f)
		}
	}
}

func TestAPasswordThatCannotBeMadeUpIsAPasswordFileWriteError(t *testing.T) {
	w := newWorld(t)
	w.conn.Random = strings.NewReader("")
	if f := w.conn.Connect(t.Context()); f == nil || f.Reason != refusal.PasswordFileWrite {
		t.Fatalf("failure %v", f)
	}
}

func TestAPasswordTheOperatorCannotGiveEndsTheCommand(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "other"
	w.conn.AskPassword = func() ([]byte, *refusal.Failure) {
		return nil, refusal.Fail(refusal.SecretSourceMissing, "no terminal")
	}
	if f := w.conn.Connect(t.Context()); f == nil || f.Reason != refusal.SecretSourceMissing {
		t.Fatalf("failure %v", f)
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
}

func TestAnAskedPasswordThatCannotBeStagedIsAWriteError(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	w.conn.AskPassword = func() ([]byte, *refusal.Failure) {
		w.conn.FS = failingCreate{match: "restic-extra.pass"}
		return []byte("asked\n"), nil
	}
	if f := w.conn.Connect(t.Context()); f == nil || f.Reason != refusal.ConfigWrite {
		t.Fatalf("failure %v", f)
	}
}

func TestOnlyTheFirstAccessIsLimitedByTheConnectTimeout(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	clock := &stepClock{fire: make(chan time.Time, 1)}
	w.conn.Bound = repoconnect.Bound{Clock: clock, Timeout: time.Minute}
	if f := w.conn.Connect(t.Context()); f != nil {
		t.Fatal(f)
	}
	if len(w.repo.files) != 2 || len(clock.asked) != 1 {
		t.Fatalf("restic ran %d times, the clock was asked %v", len(w.repo.files), clock.asked)
	}
}

func TestAWrongPasswordNobodyChoseIsNotFinalButAWrongGivenOneIs(t *testing.T) {
	w := newWorld(t)
	w.repo.initialized, w.repo.password = true, "asked"
	w.conn.AskPassword = func() ([]byte, *refusal.Failure) { return []byte("still wrong\n"), nil }
	f := w.conn.Connect(t.Context())
	if f == nil || f.Reason != refusal.WrongPassword {
		t.Fatalf("failure %v", f)
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
}

func TestAFailedCommitOfThePasswordFileIsAWriteErrorAndLeavesNoTemporaryFile(t *testing.T) {
	w := newWorld(t)
	w.conn.FS = failingFS{failRename: "restic-extra.pass"}
	f := w.conn.Connect(t.Context())
	if f == nil || f.Reason != refusal.ConfigWrite || !strings.Contains(f.Detail, "restic-extra.pass") {
		t.Fatalf("failure %v", f)
	}
	if names := w.leftovers(); len(names) != 0 {
		t.Fatalf("files %v", names)
	}
	if w.repo.inits != 0 {
		t.Fatal("init ran")
	}
}
