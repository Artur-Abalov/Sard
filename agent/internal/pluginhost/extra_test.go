// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// seqRepo is a fake repository whose n-th backup answers with sums[n] and
// err[n]; the stream of each request is kept.
type seqRepo struct {
	repo
	sums   []restic.BackupSummary
	errs   map[int]error
	inputs []string
}

func (r *seqRepo) Backup(ctx context.Context, req restic.BackupRequest, _ func(restic.Progress)) (restic.BackupSummary, error) {
	n := len(r.requests)
	r.requests = append(r.requests, req)
	var in bytes.Buffer
	if req.Stdin != nil {
		if err := req.Stdin(ctx, &in); err != nil {
			return restic.BackupSummary{}, err
		}
	}
	r.inputs = append(r.inputs, in.String())
	return r.sums[n], r.errs[n]
}

// withExtra is a plugin dumping a stream plus the extra files.
func withExtra(extra ...sdk.ExtraFile) *plugin {
	return &plugin{
		dump: func(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
			return sdk.Dump{Filename: "db.dump", Tags: map[string]string{"part": "database"}, Extra: extra}, nil
		},
		stream: func(_ context.Context, _ sdk.Host, _ sdk.Config, _ sdk.Dump, w io.Writer) error {
			_, err := io.WriteString(w, "DUMP")
			return err
		},
	}
}

var globals = sdk.ExtraFile{Name: "db.globals.sql", Content: []byte("CREATE ROLE r;"), Tags: map[string]string{"part": "globals", "format": "plain"}}

// F1 ПГ17a, ПГ17e: after the main snapshot each extra file is stored as a
// snapshot of its own, with the tags of the step, of the file and the link to
// the main snapshot (not the tags of the main dump).
func TestExtraFileIsStoredAsASnapshotLinkedToTheMainOne(t *testing.T) {
	r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: "main", RepositoryID: "rid"}, {SnapshotID: "globals", RepositoryID: "rid"}}}
	if _, err := newSource(t, withExtra(globals)).Backup(context.Background(), []byte(`{}`), r, []string{"run=7"}, &reporter{}); err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 2 {
		t.Fatalf("restic backups = %d, want 2", len(r.requests))
	}
	if got, want := r.requests[0].Tags, []string{"run=7", "fake.part=database"}; !slices.Equal(got, want) {
		t.Errorf("main tags = %q, want %q", got, want)
	}
	second := r.requests[1]
	wantTags := []string{"run=7", "fake.format=plain", "fake.part=globals", "fake.main_snapshot=main"}
	if second.StdinFilename != "db.globals.sql" || !slices.Equal(second.Tags, wantTags) || r.inputs[1] != "CREATE ROLE r;" || r.inputs[0] != "DUMP" {
		t.Errorf("second request = %+v (%q), inputs %q", second, second.Tags, r.inputs)
	}
}

// F1 ПГ17g: the bytes of the result are the sums over the snapshots; the
// snapshot and the repository are the main ones.
func TestResultOfAStepWithExtraFilesSumsTheBytes(t *testing.T) {
	r := &seqRepo{sums: []restic.BackupSummary{
		{SnapshotID: "main", RepositoryID: "rid", TotalBytes: 100, AddedBytes: 10, AddedBytesRaw: 20},
		{SnapshotID: "globals", RepositoryID: "rid", TotalBytes: 5, AddedBytes: 3, AddedBytesRaw: 4},
	}}
	sum, err := newSource(t, withExtra(globals)).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SnapshotID != "main" || sum.RepositoryID != "rid" || sum.TotalBytes != 105 || sum.AddedBytes != 13 || sum.AddedBytesRaw != 24 {
		t.Errorf("summary = %+v", sum)
	}
}

// F1 ПГ17f: the extra file is never stored before the main snapshot, and not
// at all when the main one fails.
func TestExtraFilesAreNotStoredWhenTheMainSnapshotFails(t *testing.T) {
	r := &seqRepo{sums: make([]restic.BackupSummary, 2), errs: map[int]error{0: errors.New("boom")}}
	if _, err := newSource(t, withExtra(globals)).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{}); err == nil {
		t.Fatal("no error")
	}
	if len(r.requests) != 1 {
		t.Errorf("restic backups = %d, want 1", len(r.requests))
	}
}

// F1 ПГ17f: a failed extra file leaves the main snapshot in the result, names
// the file and keeps the reason; it is a failure of the repository.
func TestFailedExtraFileKeepsTheMainSnapshotAndNamesTheFile(t *testing.T) {
	cause := errors.New("exit code 1: Fatal: unable to save")
	r := &seqRepo{
		sums: []restic.BackupSummary{{SnapshotID: "main", RepositoryID: "rid", TotalBytes: 100}, {}},
		errs: map[int]error{1: cause},
	}
	sum, err := newSource(t, withExtra(globals)).Backup(context.Background(), []byte(`{}`), r, nil, &reporter{})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "db.globals.sql") {
		t.Fatalf("err = %v", err)
	}
	if sum.SnapshotID != "main" || sum.TotalBytes != 100 {
		t.Errorf("summary = %+v", sum)
	}
}
