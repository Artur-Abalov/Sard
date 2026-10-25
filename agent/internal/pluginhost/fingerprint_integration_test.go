// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/fingerprint"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// V2 against the pinned restic: the fingerprint of files is a snapshot of its
// own tagged with its hash, the main snapshot keeps its id, restic's totals
// are in the main snapshot, and a copy restored by restic matches.

// fingerprintTree is the tree D with what restic does not count as files or
// leaves out: a link, an empty directory, an excluded file.
func fingerprintTree(t *testing.T) string {
	t.Helper()
	d := tree(t)
	writeFile(t, filepath.Join(d, "skip.log"), "log")
	if err := os.Mkdir(filepath.Join(d, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	return d
}

func fingerprintConfig(d string) string {
	b, _ := json.Marshal(map[string]any{"paths": []string{d}, "exclude": []string{"*.log"}, "control_paths": []string{filepath.Join(d, "sub")}})
	return string(b)
}

// snapshotSummary is the summary restic keeps in a snapshot (restic >= 0.17).
func (r itRepo) snapshotSummary(t *testing.T, id string) (files, bytes uint64) {
	t.Helper()
	var out []struct {
		ID      string `json:"id"`
		Summary struct {
			Files uint64 `json:"total_files_processed"`
			Bytes uint64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(r.resticOut(t, "snapshots", id)), &out); err != nil || len(out) != 1 {
		t.Fatalf("snapshots %s: %v %+v", id, err, out)
	}
	return out[0].Summary.Files, out[0].Summary.Bytes
}

func TestIntegrationTheFingerprintOfFilesIsASnapshotTaggedWithItsHash(t *testing.T) {
	r, d := newRepo(t), fingerprintTree(t)
	got := r.backup(t, fingerprintConfig(d), "sard.source=s1")
	if got.FingerprintSHA256 != sdk.HashBytes(got.Fingerprint) || len(got.Fingerprint) == 0 {
		t.Fatalf("result = %+v", got)
	}
	fp := r.fingerprintSnapshot(t, got)
	if stored := r.resticOut(t, "dump", fp, "/sard-fingerprint.json"); stored != string(got.Fingerprint) {
		t.Errorf("stored fingerprint = %q", stored)
	}
	f, err := sdk.DecodeFingerprint(got.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	// restic keeps its totals in the main snapshot: those of the fingerprint.
	files, bytes := r.snapshotSummary(t, got.SnapshotID)
	want := []sdk.Fact{
		{Name: "bytes", Rule: sdk.RuleExact, Value: strconv.FormatUint(bytes, 10)},
		{Name: "files", Rule: sdk.RuleExact, Value: strconv.FormatUint(files, 10)},
	}
	gotFiles, _ := f.Lookup("files")
	gotBytes, _ := f.Lookup("bytes")
	if !slices.Equal([]sdk.Fact{gotBytes, gotFiles}, want) || got.TotalFiles != files || bytes != 1024 {
		t.Errorf("totals: fingerprint %v %v, snapshot %d files %d bytes, result %d", gotFiles, gotBytes, files, bytes, got.TotalFiles)
	}
}

// fingerprintSnapshot checks the two snapshots of a backup of files: the main
// one as restic made it (not rewritten, no tag added) and that of the
// fingerprint, tagged with its hash and linked to the main one; it returns the
// id of the latter.
func (r itRepo) fingerprintSnapshot(t *testing.T, got pluginhost.Backup) string {
	t.Helper()
	all := r.snapshots(t)
	if len(all) != 2 {
		t.Fatalf("snapshots = %+v", all)
	}
	var fp string
	for _, s := range all {
		if !slices.Contains(s.Tags, "files.part=fingerprint") {
			r.wantMainSnapshot(t, s.ID, s.Tags, got.SnapshotID)
			continue
		}
		fp = s.ID
		want := []string{"sard.source=s1", "files.main_snapshot=" + got.SnapshotID, "files.part=fingerprint", "sard.fp=" + got.FingerprintSHA256}
		if !slices.Equal(s.Tags, want) {
			t.Errorf("tags = %q, want %q", s.Tags, want)
		}
	}
	return fp
}

func (itRepo) wantMainSnapshot(t *testing.T, id string, tags []string, want string) {
	t.Helper()
	if id != want || slices.ContainsFunc(tags, func(t string) bool { return strings.HasPrefix(t, "sard.fp=") }) {
		t.Errorf("main snapshot %s %q, result %s", id, tags, want)
	}
}

// A copy restored by restic matches the fingerprint: the totals restic counted
// are those of a walk of the copy (a link and an empty directory are no files);
// a file of the sample changed in the copy is a mismatch.
func TestIntegrationACopyRestoredByResticMatchesTheFingerprintOfFiles(t *testing.T) {
	r, d := newRepo(t), fingerprintTree(t)
	got := r.backup(t, fingerprintConfig(d))
	src, err := sdk.DecodeFingerprint(got.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	copyDir := t.TempDir()
	if err := r.cli.Restore(context.Background(), got.SnapshotID, copyDir); err != nil {
		t.Fatal(err)
	}
	p := filesplugin.Plugin{}
	measured, err := p.Fingerprint(context.Background(), nil, nil, sdk.FingerprintTarget{Root: copyDir, Reference: &src})
	if err != nil {
		t.Fatal(err)
	}
	if res := fingerprint.Compare(src, measured); !res.Matches() {
		t.Fatalf("Compare = %+v\nsource %s", res, got.Fingerprint)
	}
	writeFile(t, filepath.Join(copyDir, d, "sub", "b.txt"), strings.Repeat("y", 24))
	measured, _ = p.Fingerprint(context.Background(), nil, nil, sdk.FingerprintTarget{Root: copyDir, Reference: &src})
	res := fingerprint.Compare(src, measured)
	if len(res.Mismatches) != 1 || res.Mismatches[0].Name != "sha256:"+filepath.Join(d, "sub", "b.txt") {
		t.Errorf("Compare = %+v", res)
	}
}
