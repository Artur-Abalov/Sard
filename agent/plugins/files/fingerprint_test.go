// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/fingerprint"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var t0 = time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// trees maps the root of a tree to its files: "/" is the host, other roots are copies.
type trees map[string]fstest.MapFS

func (t trees) open(root string) fs.FS { return t[root] }

func source() fstest.MapFS {
	return fstest.MapFS{
		"srv/data/a.txt":         {Data: []byte("alpha"), ModTime: t0},
		"srv/data/b.txt":         {Data: []byte("beta"), ModTime: t0},
		"srv/data/sub/c.txt":     {Data: []byte("gamma"), ModTime: t0},
		"srv/data/skip.log":      {Data: []byte("log"), ModTime: t0},
		"srv/data/big.bin":       {Data: []byte(strings.Repeat("x", 100)), ModTime: t0},
		"srv/data/empty":         {Mode: fs.ModeDir},
		"srv/data/link":          {Mode: fs.ModeSymlink, Data: []byte("a.txt")},
		"srv/data/marker":        {Data: []byte("m"), ModTime: t0},
		"srv/other/zero":         {ModTime: t0},
		"srv/data/sub/deep/d.md": {Data: []byte("delta"), ModTime: t0},
	}
}

// firstN is a Rand that keeps the first files of the walk: it never replaces.
func firstN(n int) int { return n - 1 }

func plugin(tr trees) files.Plugin {
	return files.Plugin{Tree: tr.open, Rand: firstN, Now: func() time.Time { return t0 }}
}

func cfgJSON(extra string) sdk.Config {
	return sdk.Config(`{"paths": ["/srv/data"], "exclude": ["*.log"], "sample_max_file_bytes": 50` + extra + `}`)
}

func facts(f sdk.Fingerprint, prefix string) map[string]sdk.Fact {
	out := map[string]sdk.Fact{}
	for _, x := range f.Facts {
		if strings.HasPrefix(x.Name, prefix) {
			out[x.Name] = x
		}
	}
	return out
}

// The sample hashes regular files of the paths only: not an excluded file, a
// file larger than the limit, a link or a directory.
func TestTheSampleHashesTheFilesResticStores(t *testing.T) {
	f, err := plugin(trees{"/": source()}).Fingerprint(context.Background(), nil, cfgJSON(""), sdk.FingerprintTarget{})
	if err != nil {
		t.Fatal(err)
	}
	if (f.Plugin != "files") || (f.Format != sdk.FingerprintFormat) {
		t.Errorf("fingerprint = %+v", f)
	}
	want := map[string]sdk.Fact{}
	for path, content := range map[string]string{"a.txt": "alpha", "b.txt": "beta", "marker": "m", "sub/c.txt": "gamma", "sub/deep/d.md": "delta"} {
		name := "sha256:/srv/data/" + path
		want[name] = sdk.Fact{Name: name, Rule: sdk.RuleExact, Value: sum(content)}
	}
	if got := facts(f, "sha256:"); !maps.Equal(got, want) {
		t.Errorf("sampled %v\nwant %v", got, want)
	}
	if s, _ := f.Lookup("sample"); s != (sdk.Fact{Name: "sample", Rule: sdk.RuleInfo, Value: "5 of 5 files hashed"}) {
		t.Errorf("sample = %+v", s)
	}
}

func TestTheSampleHasAtMostTheFilesAsked(t *testing.T) {
	f, err := plugin(trees{"/": source()}).Fingerprint(context.Background(), nil, cfgJSON(`, "sample_files": 2`), sdk.FingerprintTarget{})
	if err != nil {
		t.Fatal(err)
	}
	if got := facts(f, "sha256:"); len(got) != 2 {
		t.Errorf("sampled %v", got)
	}
	if s, _ := f.Lookup("sample"); s.Value != "2 of 5 files hashed" {
		t.Errorf("sample = %+v", s)
	}
	f, _ = plugin(trees{"/": source()}).Fingerprint(context.Background(), nil, cfgJSON(`, "sample_files": 0`), sdk.FingerprintTarget{})
	if got := facts(f, "sha256:"); len(got) != 0 {
		t.Errorf("sample off: %v", got)
	}
}

// The reservoir keeps a uniform choice: Rand decides which files replace the first.
func TestTheSampleIsChosenByRand(t *testing.T) {
	p := plugin(trees{"/": source()})
	var asked []int
	p.Rand = func(n int) int { asked = append(asked, n); return 0 }
	f, _ := p.Fingerprint(context.Background(), nil, cfgJSON(`, "sample_files": 1`), sdk.FingerprintTarget{})
	if !slices.Equal(asked, []int{2, 3, 4, 5}) {
		t.Errorf("Rand asked %v", asked)
	}
	// Each later file replaced the first: the last one walked is kept.
	if got := facts(f, "sha256:"); len(got) != 1 || got["sha256:/srv/data/sub/deep/d.md"].Value != sum("delta") {
		t.Errorf("sampled %v", got)
	}
}

func TestTheWalkStopsAtItsTimeLimitAndSaysSo(t *testing.T) {
	p := plugin(trees{"/": source()})
	now := t0
	p.Now = func() time.Time { now = now.Add(time.Minute); return now }
	p.WalkLimit = 3 * time.Minute
	f, err := p.Fingerprint(context.Background(), nil, cfgJSON(""), sdk.FingerprintTarget{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := f.Lookup("sample")
	if !strings.Contains(s.Value, "the walk stopped at its time limit") || len(facts(f, "sha256:")) >= 5 {
		t.Errorf("sample = %q, %v", s.Value, facts(f, "sha256:"))
	}
}

func TestTheReadLimitBoundsTheHashedBytes(t *testing.T) {
	p := plugin(trees{"/": source()})
	p.ReadLimit = 9 // alpha and beta
	f, _ := p.Fingerprint(context.Background(), nil, cfgJSON(""), sdk.FingerprintTarget{})
	got := facts(f, "sha256:")
	if len(got) != 2 || got["sha256:/srv/data/a.txt"].Value == "" || got["sha256:/srv/data/b.txt"].Value == "" {
		t.Errorf("sampled %v", got)
	}
	if s, _ := f.Lookup("sample"); !strings.Contains(s.Value, "the read limit was reached") {
		t.Errorf("sample = %q", s.Value)
	}
}

func TestACancelledWalkIsTheErrorOfTheFingerprint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := plugin(trees{"/": source()}).Fingerprint(ctx, nil, cfgJSON(""), sdk.FingerprintTarget{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestAnInvalidConfigIsTheErrorOfTheFingerprint(t *testing.T) {
	_, err := plugin(trees{"/": source()}).Fingerprint(context.Background(), nil, sdk.Config(`{"paths": ["/a"], "control_paths": ["/b"]}`), sdk.FingerprintTarget{})
	if !errors.Is(err, sdk.ErrInvalidConfig) || !strings.Contains(err.Error(), "lies inside none of the paths") {
		t.Errorf("err = %v", err)
	}
}

func TestControlPathsAreFacts(t *testing.T) {
	f, err := plugin(trees{"/": source()}).Fingerprint(context.Background(), nil, cfgJSON(`, "control_paths": ["/srv/data/marker", "/srv/data/sub"]`), sdk.FingerprintTarget{})
	if err != nil {
		t.Fatal(err)
	}
	got := facts(f, "control:")
	if len(got) != 2 || got["control:/srv/data/marker"].Value != "nonempty" || got["control:/srv/data/sub"] != (sdk.Fact{Name: "control:/srv/data/sub", Rule: sdk.RuleExact, Value: "nonempty"}) {
		t.Errorf("control = %v", got)
	}
}

// --- the check before restic

func TestAMissingOrEmptyControlPathFailsTheBackupBeforeRestic(t *testing.T) {
	tree := source()
	tree["srv/data/zero"] = &fstest.MapFile{ModTime: t0}
	for name, tc := range map[string]struct {
		path, want string
	}{
		"missing":         {"/srv/data/gone", `"/srv/data/gone": control path: no such file or directory`},
		"empty directory": {"/srv/data/empty", `"/srv/data/empty": control path: empty`},
		"empty file":      {"/srv/data/zero", `"/srv/data/zero": control path: empty`},
	} {
		p := plugin(trees{"/": tree})
		p.FS = readable()
		err := p.Prepare(context.Background(), nil, sdk.Config(`{"paths": ["/srv/data"], "control_paths": ["`+tc.path+`"]}`))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	p := plugin(trees{"/": tree})
	p.FS = readable()
	if err := p.Prepare(context.Background(), nil, sdk.Config(`{"paths": ["/srv/data"], "control_paths": ["/srv/data/marker", "/srv/data/sub"]}`)); err != nil {
		t.Errorf("present control paths: %v", err)
	}
}

// readable is the file system of the check of the paths: /srv/data is a readable directory.
func readable() *fakeFS { return newFS().dir("/srv/data") }

// --- after restic

func draft(t *testing.T, p files.Plugin, cfg sdk.Config) sdk.Dump {
	t.Helper()
	d, err := p.Dump(context.Background(), nil, cfg)
	if err != nil || d.Fingerprint == nil {
		t.Fatalf("Dump = %+v, %v", d, err)
	}
	return d
}

// The fingerprint of the backup holds restic's totals and leaves out of the
// comparison every sampled file that changed while restic read it.
func TestTheCompletedFingerprintHasTheTotalsAndDropsTheChangedFiles(t *testing.T) {
	tree := source()
	p := plugin(trees{"/": tree})
	d := draft(t, p, cfgJSON(""))
	tree["srv/data/a.txt"] = &fstest.MapFile{Data: []byte("alpha2"), ModTime: t0}                // size
	tree["srv/data/b.txt"] = &fstest.MapFile{Data: []byte("beta"), ModTime: t0.Add(time.Second)} // mtime
	delete(tree, "srv/data/marker")                                                              // gone
	f, err := p.CompleteFingerprint(context.Background(), nil, cfgJSON(""), d, sdk.BackupTotals{Files: 9, Bytes: 120})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]sdk.Fact{
		"files":                      {Name: "files", Rule: sdk.RuleExact, Value: "9"},
		"bytes":                      {Name: "bytes", Rule: sdk.RuleExact, Value: "120"},
		"sha256:/srv/data/a.txt":     {Name: "sha256:/srv/data/a.txt", Rule: sdk.RuleInfo, Value: "changed during the backup"},
		"sha256:/srv/data/b.txt":     {Name: "sha256:/srv/data/b.txt", Rule: sdk.RuleInfo, Value: "changed during the backup"},
		"sha256:/srv/data/marker":    {Name: "sha256:/srv/data/marker", Rule: sdk.RuleInfo, Value: "changed during the backup"},
		"sha256:/srv/data/sub/c.txt": {Name: "sha256:/srv/data/sub/c.txt", Rule: sdk.RuleExact, Value: sum("gamma")},
	} {
		if got, _ := f.Lookup(name); got != want {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
	if _, err := f.Encode(); err != nil {
		t.Error(err)
	}
}

func TestADumpWithoutADraftCompletesToTheTotals(t *testing.T) {
	f, err := plugin(trees{"/": source()}).CompleteFingerprint(context.Background(), nil, cfgJSON(""), sdk.Dump{}, sdk.BackupTotals{Files: 1, Bytes: 2})
	if err != nil || len(f.Facts) != 2 || f.Plugin != "files" {
		t.Errorf("CompleteFingerprint = %+v, %v", f, err)
	}
}

// --- a copy

// restored is the copy of source as restic restores it under /restore/c1.
func restored() fstest.MapFS {
	tree := fstest.MapFS{}
	for name, f := range source() {
		if name != "srv/data/skip.log" && name != "srv/other/zero" {
			tree[name] = f
		}
	}
	return tree
}

func copyOf(t *testing.T, tr trees, src sdk.Fingerprint) sdk.Fingerprint {
	t.Helper()
	f, err := plugin(tr).Fingerprint(context.Background(), nil, nil, sdk.FingerprintTarget{Root: "/restore/c1", Reference: &src})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func sourceFingerprint(t *testing.T) sdk.Fingerprint {
	t.Helper()
	p := plugin(trees{"/": source()})
	cfg := cfgJSON(`, "control_paths": ["/srv/data/marker", "/srv/data/sub"]`)
	f, err := p.CompleteFingerprint(context.Background(), nil, cfg, draft(t, p, cfg), sdk.BackupTotals{Files: 6, Bytes: 120})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The same Fingerprint, pointed at a copy, measures the facts of the source;
// a faithful copy matches.
func TestAFaithfulCopyMatchesTheSource(t *testing.T) {
	src := sourceFingerprint(t)
	got := copyOf(t, trees{"/restore/c1": restored()}, src)
	if r := fingerprint.Compare(src, got); !r.Matches() {
		t.Errorf("Compare = %+v\ncopy %+v", r, got)
	}
}

// A file of the sample replaced in the copy, an emptied control directory or
// a missing file are mismatches that name them.
func TestAChangedCopyIsAMismatchNamingWhatChanged(t *testing.T) {
	src := sourceFingerprint(t)
	tree := restored()
	tree["srv/data/sub/c.txt"] = &fstest.MapFile{Data: []byte("GAMMA"), ModTime: t0}
	delete(tree, "srv/data/marker")
	got := copyOf(t, trees{"/restore/c1": tree}, src)
	r := fingerprint.Compare(src, got)
	var texts []string
	for _, m := range r.Mismatches {
		texts = append(texts, m.Text())
	}
	want := []string{
		"bytes: 120 in the source, 119 in the copy",
		"control:/srv/data/marker: nonempty in the source, missing in the copy",
		"files: 6 in the source, 5 in the copy",
		"sha256:/srv/data/marker: " + sum("m") + " in the source, missing in the copy",
		"sha256:/srv/data/sub/c.txt: " + sum("gamma") + " in the source, " + sum("GAMMA") + " in the copy",
	}
	if !slices.Equal(texts, want) {
		t.Errorf("mismatches:\n%s\nwant\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
}

// A file that changed during the backup is not judged in the copy.
func TestAFileChangedDuringTheBackupGivesNoFalseMismatch(t *testing.T) {
	tree := source()
	p := plugin(trees{"/": tree})
	cfg := cfgJSON("")
	d := draft(t, p, cfg)
	tree["srv/data/a.txt"] = &fstest.MapFile{Data: []byte("ALPHA!"), ModTime: t0.Add(time.Second)}
	src, err := p.CompleteFingerprint(context.Background(), nil, cfg, d, sdk.BackupTotals{Files: 6, Bytes: 121})
	if err != nil {
		t.Fatal(err)
	}
	copied := restored()
	copied["srv/data/a.txt"] = &fstest.MapFile{Data: []byte("ALPHA!"), ModTime: t0.Add(time.Second)}
	if r := fingerprint.Compare(src, copyOf(t, trees{"/restore/c1": copied}, src)); !r.Matches() {
		t.Errorf("Compare = %+v", r)
	}
}

func TestACopyIsMeasuredOnlyByTheFactsOfTheReference(t *testing.T) {
	ref := sdk.Fingerprint{Plugin: "files", Format: 1, Facts: []sdk.Fact{{Name: "sha256:/srv/data/a.txt", Rule: sdk.RuleExact, Value: sum("alpha")}}}
	got := copyOf(t, trees{"/restore/c1": restored()}, ref)
	if len(got.Facts) != 1 || got.Facts[0].Value != sum("alpha") {
		t.Errorf("copy = %+v", got)
	}
}

// RestoreInto restores the whole snapshot as a tree through the host; the copy is measured there.
func TestRestoreIntoRestoresTheTreeThroughTheHost(t *testing.T) {
	h := &treeHost{root: "/restore/c1"}
	c, err := files.Plugin{}.RestoreInto(context.Background(), h, sdk.Config(`{}`), sdk.RestoreRequest{SnapshotID: snapshotID})
	if err != nil || c.Root != "/restore/c1" || c.Result.Target != "/restore/c1" || !slices.Equal(h.asked, []string{snapshotID}) {
		t.Errorf("RestoreInto = %+v, %v (asked %v)", c, err, h.asked)
	}
	h.err = errors.New("WRONG_PASSWORD")
	if _, err := (files.Plugin{}).RestoreInto(context.Background(), h, nil, sdk.RestoreRequest{SnapshotID: snapshotID}); !errors.Is(err, h.err) {
		t.Errorf("err = %v", err)
	}
}

type treeHost struct {
	sdk.RestoreHost
	root  string
	err   error
	asked []string
}

func (h *treeHost) RestoreTree(_ context.Context, id string) (string, error) {
	h.asked = append(h.asked, id)
	if h.err != nil {
		return "", h.err
	}
	return h.root, nil
}
