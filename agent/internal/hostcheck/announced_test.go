// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// Rule "repository id сравнивается с объявленным службой серверу" (Ч4).

func TestTheRecordOfAnnouncedRepositoriesIsReadBack(t *testing.T) {
	dir := t.TempDir()
	want := []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "REPO-A"}, {Name: "obj"}}
	if err := hostcheck.WriteAnnounced(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := hostcheck.ReadAnnounced(os.ReadFile, dir)
	if err != nil || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestTheRecordIsOwnerOnlyAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	for range 2 { // the second write replaces the first
		if err := hostcheck.WriteAnnounced(dir, nil); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != hostcheck.AnnouncedFile {
		t.Fatalf("directory holds %v", entries)
	}
	info, _ := os.Stat(filepath.Join(dir, hostcheck.AnnouncedFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestAFailedWriteLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, hostcheck.AnnouncedFile), 0o700); err != nil { // the target cannot be replaced
		t.Fatal(err)
	}
	if err := hostcheck.WriteAnnounced(dir, nil); err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("directory holds %v", entries)
	}
	if err := hostcheck.WriteAnnounced(filepath.Join(dir, "missing"), nil); err == nil {
		t.Fatal("writing into a missing directory succeeded")
	}
}

func TestAMissingOrBrokenRecordIsAnError(t *testing.T) {
	dir := t.TempDir()
	if _, err := hostcheck.ReadAnnounced(os.ReadFile, dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostcheck.AnnouncedFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostcheck.ReadAnnounced(os.ReadFile, dir); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("broken: %v", err)
	}
}

func TestTheIdentifierMatchesTheAnnouncedOne(t *testing.T) {
	ann := []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "REPO-A"}}
	l := hostcheck.IDLine("base", "REPO-A", ann, nil)
	if l.Status != hostcheck.OK || l.Check != "repository.id" || l.Subject != "base" || !strings.Contains(l.Message, "REPO-A") || l.Hint != "" {
		t.Fatalf("%+v", l)
	}
}

func TestTheIdentifierThatDiffersFromTheAnnouncedOneIsAnError(t *testing.T) {
	ann := []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "REPO-B"}}
	l := hostcheck.IDLine("base", "REPO-A", ann, nil)
	if l.Status != hostcheck.Error || !strings.Contains(l.Message, "REPO-B") || !strings.Contains(l.Message, "REPO-A") {
		t.Fatalf("%+v", l)
	}
	if !strings.Contains(l.Hint, "sudo systemctl restart sard-agent") {
		t.Errorf("hint %q", l.Hint)
	}
}

func TestAnEmptyAnnouncedIdentifierAndAMissingEntryAreWarnings(t *testing.T) {
	empty := hostcheck.IDLine("base", "REPO-A", []hostcheck.AnnouncedRepository{{Name: "base"}}, nil)
	other := hostcheck.IDLine("base", "REPO-A", []hostcheck.AnnouncedRepository{{Name: "other", RepositoryID: "X"}}, nil)
	if empty.Status != hostcheck.Warn || !strings.Contains(empty.Message, "could not read") {
		t.Errorf("empty %+v", empty)
	}
	if other.Status != hostcheck.Warn || !strings.Contains(other.Message, "not announced to the server yet") {
		t.Errorf("other %+v", other)
	}
	for _, l := range []hostcheck.Line{empty, other} {
		if !strings.Contains(l.Hint, "sudo systemctl restart sard-agent") {
			t.Errorf("hint %q", l.Hint)
		}
	}
}

func TestWithoutARecordTheComparisonIsSkipped(t *testing.T) {
	l := hostcheck.IDLine("base", "REPO-A", nil, &fs.PathError{Op: "open", Err: fs.ErrNotExist})
	if l.Status != hostcheck.Skip || !strings.Contains(l.Hint, "sudo systemctl start sard-agent") {
		t.Fatalf("%+v", l)
	}
}

func TestARecordThatCannotBeReadIsAWarning(t *testing.T) {
	l := hostcheck.IDLine("base", "REPO-A", nil, errors.New("invalid character"))
	if l.Status != hostcheck.Warn || !strings.Contains(l.Message, "invalid character") || !strings.Contains(l.Hint, "sudo systemctl restart sard-agent") {
		t.Fatalf("%+v", l)
	}
}
