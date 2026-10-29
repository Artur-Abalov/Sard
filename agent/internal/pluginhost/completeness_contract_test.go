// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	restorelist "github.com/Artur-Abalov/sard/agent/internal/restore"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
)

// tagLister lists the snapshots a backup wrote, with the tags exactly as the
// pluginhost gave them to restic.
type tagLister []restic.Snapshot

func (l tagLister) Snapshots(context.Context, []string) ([]restic.Snapshot, error) { return l, nil }

// Contract between the writer of the tags (pluginhost, the files plugin) and
// the reader (restorelist.List, Р89): the main snapshot of a real files backup is
// confirmed by its fingerprint snapshot, which is itself not applicable.
// Renaming the plugin or the keys of withPluginTags breaks this test.
func TestTheTagsOfAFilesBackupAreReadAsConfirmedByRestore(t *testing.T) {
	mainID, fpID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	r := &seqRepo{sums: []restic.BackupSummary{{SnapshotID: mainID, TotalFiles: 1}, {SnapshotID: fpID}}}
	src, err := pluginhost.NewSource(filesplugin.Plugin{AgentVersion: "test"}, pluginhost.NewSecrets(nil, os.ReadFile, nil))
	if err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"paths":[%q]}`, t.TempDir())
	step := []string{"sard.source=docs", "sard.run=r1", "sard.step=s1"}
	if _, err := src.Backup(context.Background(), []byte(cfg), r, step, &reporter{}); err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 2 {
		t.Fatalf("requests = %d, want the main and the fingerprint", len(r.requests))
	}
	listed, err := restorelist.List(context.Background(), tagLister{
		{ID: mainID, Tags: r.requests[0].Tags},
		{ID: fpID, Tags: r.requests[1].Tags},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]restorelist.Completeness{}
	for _, s := range listed {
		got[s.ID] = s.Completeness
	}
	if got[mainID] != restorelist.Confirmed || got[fpID] != restorelist.NotApplicable {
		t.Errorf("completeness = %q, want main confirmed and fingerprint not applicable (tags %q / %q)", got, r.requests[0].Tags, r.requests[1].Tags)
	}
}
