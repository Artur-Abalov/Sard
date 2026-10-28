// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

var sourceFP = sdk.Fingerprint{Plugin: "fake", Format: 1, Facts: []sdk.Fact{{Name: "tables", Rule: sdk.RuleExact, Value: "3"}}}

func mustEncode(t *testing.T, f sdk.Fingerprint) ([]byte, string) {
	t.Helper()
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b, sdk.HashBytes(b)
}

// fingerprinted is a plugin dumping a stream with fp as the fingerprint, and the globals.
func fingerprinted(fp *sdk.Fingerprint) *plugin {
	p := withExtra(globals)
	dump := p.dump
	p.dump = func(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
		d, err := dump(ctx, h, cfg)
		d.Fingerprint = fp
		return d, err
	}
	return p
}

// V2: the hash of a fingerprint taken before restic is a tag of the main
// snapshot when restic creates it; the extra snapshots do not carry it.
func TestTheHashOfTheFingerprintIsATagOfTheMainSnapshot(t *testing.T) {
	fp := sourceFP
	b, hash := mustEncode(t, fp)
	r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: "main", TotalFiles: 1}, {SnapshotID: "globals"}}}
	got, err := newSource(t, fingerprinted(&fp)).Backup(context.Background(), []byte(`{}`), r, []string{"run=7"}, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"run=7", "fake.part=database", "sard.fp=" + hash}; !slices.Equal(r.requests[0].Tags, want) {
		t.Errorf("main tags = %q, want %q", r.requests[0].Tags, want)
	}
	if slices.ContainsFunc(r.requests[1].Tags, func(t string) bool { return strings.HasPrefix(t, "sard.fp=") }) {
		t.Errorf("globals tags = %q", r.requests[1].Tags)
	}
	if string(got.Fingerprint) != string(b) || got.FingerprintSHA256 != hash || got.SnapshotID != "main" {
		t.Errorf("result = %+v", got)
	}
}

func TestABackupWithoutAFingerprintHasNoTagAndNoFingerprint(t *testing.T) {
	r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: "main"}, {SnapshotID: "globals"}}}
	got, err := newSource(t, fingerprinted(nil)).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{})
	if err != nil || got.Fingerprint != nil || got.FingerprintSHA256 != "" {
		t.Fatalf("result = %+v, %v", got, err)
	}
	for _, req := range r.requests {
		if slices.ContainsFunc(req.Tags, func(t string) bool { return strings.HasPrefix(t, "sard.") }) {
			t.Errorf("tags = %q", req.Tags)
		}
	}
}

// An invalid fingerprint, or a step that already has the tag, fails before restic runs.
func TestAFingerprintThatCannotBeTaggedFailsBeforeRestic(t *testing.T) {
	bad := sdk.Fingerprint{Plugin: "fake"}
	for name, tc := range map[string]struct {
		fp   *sdk.Fingerprint
		tags []string
		want string
	}{
		"invalid":         {&bad, nil, "fingerprint of the plugin"},
		"tag of the step": {&sourceFP, []string{"sard.fp=x"}, "sard.fp"},
	} {
		r := &seqRepo{sums: make([]restic.BackupSummary, 2)}
		_, err := newSource(t, fingerprinted(tc.fp)).Backup(context.Background(), []byte(`{}`), r, tc.tags, &reporter{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(r.requests) != 0 {
			t.Errorf("%s: restic ran", name)
		}
	}
}

// completer is a plugin by paths whose fingerprint is completed after restic.
type completer struct {
	*plugin
	got      []sdk.BackupTotals
	draft    []*sdk.Fingerprint
	complete func() (sdk.Fingerprint, error)
}

func (c *completer) CompleteFingerprint(_ context.Context, h sdk.Host, _ sdk.Config, d sdk.Dump, totals sdk.BackupTotals) (sdk.Fingerprint, error) {
	if h == nil {
		return sdk.Fingerprint{}, errors.New("no host")
	}
	c.got = append(c.got, totals)
	c.draft = append(c.draft, d.Fingerprint)
	return c.complete()
}

func newCompleter() *completer {
	draft := sourceFP
	p := &plugin{dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
		return sdk.Dump{Paths: []string{"/srv"}, Fingerprint: &draft}, nil
	}}
	done := sdk.Fingerprint{Plugin: "fake", Format: 1, Facts: []sdk.Fact{{Name: "files", Rule: sdk.RuleExact, Value: "4"}}}
	return &completer{plugin: p, complete: func() (sdk.Fingerprint, error) { return done, nil }}
}

// V2 (files): a fingerprint known only after restic is stored as a snapshot of
// its own, linked to the main one and tagged with its hash; the main snapshot
// is not rewritten and has no tag of a fingerprint.
func TestACompletedFingerprintIsASnapshotOfItsOwnLinkedToTheMainOne(t *testing.T) {
	c := newCompleter()
	want, _ := c.complete()
	b, hash := mustEncode(t, want)
	r := &seqRepo{sums: []restic.BackupSummary{
		{SnapshotID: "main", RepositoryID: "rid", TotalBytes: 100, TotalFiles: 4, AddedBytes: 10, AddedBytesRaw: 12},
		{SnapshotID: "fp", RepositoryID: "rid", TotalBytes: 50, AddedBytes: 3, AddedBytesRaw: 5},
	}}
	got, err := newSource(t, c).Backup(context.Background(), []byte(`{}`), r, []string{"run=7"}, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.requests[0].Tags, []string{"run=7"}) {
		t.Errorf("main tags = %q", r.requests[0].Tags)
	}
	if !slices.Equal(c.got, []sdk.BackupTotals{{Files: 4, Bytes: 100}}) || len(c.draft) != 1 || c.draft[0] == nil || c.draft[0].Facts[0].Name != "tables" {
		t.Errorf("CompleteFingerprint got %+v, draft %v", c.got, c.draft)
	}
	wantFingerprintSnapshot(t, r, []string{"run=7", "fake.main_snapshot=main", "fake.part=fingerprint", "sard.fp=" + hash}, b)
	// The totals stay those of the main snapshot; the repository grew by both.
	type outcome struct {
		snapshot                                     string
		totalBytes, totalFiles, addedBytes, addedRaw uint64
		fingerprint, hash                            string
	}
	gotOut := outcome{got.SnapshotID, got.TotalBytes, got.TotalFiles, got.AddedBytes, got.AddedBytesRaw, string(got.Fingerprint), got.FingerprintSHA256}
	if want := (outcome{"main", 100, 4, 13, 17, string(b), hash}); gotOut != want {
		t.Errorf("result = %+v, want %+v", gotOut, want)
	}
}

// wantFingerprintSnapshot: the second backup stores the fingerprint with the tags.
func wantFingerprintSnapshot(t *testing.T, r *seqRepo, tags []string, b []byte) {
	t.Helper()
	second := r.requests[1]
	if second.StdinFilename != "sard-fingerprint.json" || !slices.Equal(second.Tags, tags) || r.inputs[1] != string(b) {
		t.Errorf("fingerprint snapshot = %q %q %q", second.StdinFilename, second.Tags, r.inputs[1])
	}
}

func TestACompleterIsNotCalledWhenTheMainSnapshotFails(t *testing.T) {
	c := newCompleter()
	r := &seqRepo{sums: make([]restic.BackupSummary, 2), errs: map[int]error{0: errors.New("boom")}}
	if _, err := newSource(t, c).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{}); err == nil {
		t.Fatal("no error")
	}
	if len(c.got) != 0 || len(r.requests) != 1 {
		t.Errorf("completed %v, %d backups", c.got, len(r.requests))
	}
}

// A fingerprint that cannot be completed or stored fails the step and keeps
// the main snapshot in the result, without a fingerprint.
func TestAFailedFingerprintKeepsTheMainSnapshot(t *testing.T) {
	for name, tc := range map[string]struct {
		complete func() (sdk.Fingerprint, error)
		errs     map[int]error
		want     string
	}{
		"plugin fails":   {func() (sdk.Fingerprint, error) { return sdk.Fingerprint{}, errors.New("vanished") }, nil, "vanished"},
		"invalid":        {func() (sdk.Fingerprint, error) { return sdk.Fingerprint{Plugin: "fake"}, nil }, nil, "fingerprint of the plugin"},
		"snapshot of it": {nil, map[int]error{1: errors.New("Fatal: unable to save")}, "sard-fingerprint.json"},
		"exit code 3":    {nil, map[int]error{1: &restic.ExitError{Code: 3, Message: "stream"}}, "sard-fingerprint.json"},
	} {
		c := newCompleter()
		if tc.complete != nil {
			c.complete = tc.complete
		}
		r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: "main", TotalBytes: 100}, {}}, errs: tc.errs}
		got, err := newSource(t, c).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if got.SnapshotID != "main" || got.Fingerprint != nil || got.FingerprintSHA256 != "" {
			t.Errorf("%s: result = %+v", name, got)
		}
	}
}

func TestTheOutputOfABackupCarriesTheFingerprintAndTheNumberOfFiles(t *testing.T) {
	b, hash := mustEncode(t, sourceFP)
	got := pluginhost.BackupOutput(pluginhost.Backup{
		BackupSummary: restic.BackupSummary{SnapshotID: "s", RepositoryID: "r", TotalBytes: 9, AddedBytes: 2, TotalFiles: 3},
		Fingerprint:   b, FingerprintSHA256: hash,
	})
	want := &agentv1.BackupOutput{SnapshotId: "s", RepositoryId: "r", TotalBytes: 9, AddedBytes: 2, TotalFiles: 3, Fingerprint: b, FingerprintSha256: hash, UnreadableCount: proto.Uint64(0)}
	if !proto.Equal(got, want) {
		t.Errorf("BackupOutput = %v, want %v", got, want)
	}
}

// released is a streamed dump whose Release is counted, and noted in calls.
func released(p *plugin, n *int) *plugin {
	dump := p.dump
	p.dump = func(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
		d, err := dump(ctx, h, cfg)
		d.Release = func() { *n++; p.calls = append(p.calls, "release") }
		return d, err
	}
	return p
}

// V2: what Dump holds for Stream (the transaction of the snapshot) is
// released once, right after the main snapshot, before the extra snapshots.
func TestReleaseFollowsTheStreamOnce(t *testing.T) {
	var n int
	p := released(withExtra(globals), &n)
	r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: "main"}, {SnapshotID: "globals"}}}
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{}); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !slices.Equal(p.calls, []string{"prepare {}", "dump {}", "stream {} db.dump", "release"}) {
		t.Errorf("released %d times, calls %q", n, p.calls)
	}
}

func TestReleaseRunsWhenTheBackupGivesUpBeforeTheStream(t *testing.T) {
	var n int
	p := released(withExtra(globals), &n)
	r := &seqRepo{sums: make([]restic.BackupSummary, 2)}
	if _, err := newSource(t, p).Backup(context.Background(), []byte(`{}`), r, []string{"fake.part=x"}, &reporter{}); err == nil {
		t.Fatal("no error")
	}
	if n != 1 || len(r.requests) != 0 {
		t.Errorf("released %d times, %d backups", n, len(r.requests))
	}
}
