// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Rule "repo restore выгружает снимок плагина файлами по его плану, не зная
// базы" (R2c-3, РВ-8, О3, В10).

// extractor is a plugin of the tests that plans what it is told to.
type extractor struct {
	plan sdk.ExtractPlan
	err  error
	got  []sdk.ExtractRequest
}

func (e *extractor) PlanExtract(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	e.got = append(e.got, req)
	return e.plan, e.err
}

func tagged(prefix string, tags ...string) restore.Snapshot {
	return restore.Snapshot{ID: id(prefix), ShortID: prefix, Time: at(3).UTC(), Paths: []string{"/a"}, Tags: tags}
}

func oneFile(snapshot string) sdk.ExtractPlan {
	return sdk.ExtractPlan{Label: "X", Files: []sdk.ExtractFile{{SnapshotID: snapshot, Path: "/a", Name: "out.bin"}}, Hints: []string{"run x-load out.bin"}}
}

func TestTheFilesOfAPluginSnapshotAreThoseOfTheOnePlanningThem(t *testing.T) {
	snap := tagged("aaaa1111", "sard.source=s", "x.part=main")
	x := &extractor{plan: oneFile(snap.ID)}
	got, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": x}, []restore.Snapshot{snap}, snap, "/srv/t")
	if f != nil {
		t.Fatal(f)
	}
	want := restore.Extraction{Plugin: "x", Label: "X", Files: []restore.File{{SnapshotID: snap.ID, Path: "/a", Name: "out.bin"}}, Hints: []string{"run x-load out.bin"}}
	if got.Plugin != want.Plugin || got.Label != want.Label || !slices.Equal(got.Files, want.Files) || !slices.Equal(got.Hints, want.Hints) || got.Tree() {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// asked is the one request the plugin got.
func asked(t *testing.T, snap restore.Snapshot, list []restore.Snapshot) sdk.ExtractRequest {
	t.Helper()
	x := &extractor{plan: oneFile(snap.ID)}
	if _, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": x}, list, snap, "/srv/t"); f != nil || len(x.got) != 1 {
		t.Fatalf("%v, asked %d times", f, len(x.got))
	}
	return x.got[0]
}

func TestThePluginIsToldTheSnapshotsOfTheRepositoryAndTheTarget(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main")
	other := tagged("bbbb1111", "sard.source=s")
	req := asked(t, snap, []restore.Snapshot{other, snap})
	if req.Target != "/srv/t" || len(req.Snapshots) != 2 || req.Snapshots[0].ID != other.ID {
		t.Errorf("request %+v", req)
	}
}

func TestThePluginIsToldWhatTheSnapshotIs(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main")
	got := asked(t, snap, nil).Snapshot
	if got.ID != snap.ID || !slices.Equal(got.Tags, snap.Tags) || !slices.Equal(got.Paths, snap.Paths) || !got.Time.Equal(snap.Time) {
		t.Errorf("snapshot %+v", got)
	}
}

// Scenario: Снимок без метки плагина с выгрузкой восстанавливается деревом (О3).
func TestASnapshotWithoutATagOfAPluginIsATreeAndNoPluginIsAsked(t *testing.T) {
	x := &extractor{plan: oneFile("x")}
	for _, tags := range [][]string{nil, {"sard.source=s", "sard.run=r"}, {"xx.part=main"}, {"x=main"}, {"y.part=1"}} {
		snap := tagged("aaaa1111", tags...)
		got, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": x}, []restore.Snapshot{snap}, snap, "/t")
		if f != nil || !got.Tree() {
			t.Errorf("%v: %+v, %v", tags, got, f)
		}
	}
	if len(x.got) != 0 {
		t.Errorf("the plugin was asked %d times", len(x.got))
	}
}

func TestAPlanWithoutFilesIsATree(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=other")
	x := &extractor{}
	got, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": x}, []restore.Snapshot{snap}, snap, "/t")
	if f != nil || !got.Tree() || len(x.got) != 1 {
		t.Errorf("%+v, %v, asked %d times", got, f, len(x.got))
	}
}

// Scenario: Ошибка плана выгрузки ничего не записывает (В10).
func TestAnErrorOfThePlanIsRefusedAsTheAgentsWithTheWordsOfThePlugin(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main")
	_, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": &extractor{err: errors.New("no main file")}}, nil, snap, "/t")
	if f == nil || f.Reason != refusal.ResticOutputUnexpected || f.Class != refusal.ClassAgentError || f.Detail != "no main file" {
		t.Fatalf("failure %+v", f)
	}
}

func TestSeveralPluginsClaimingASnapshotRefuseIt(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main", "y.part=main")
	x, y := &extractor{plan: oneFile(snap.ID)}, &extractor{plan: oneFile(snap.ID)}
	_, f := restore.PlanExtraction(map[string]sdk.Extractor{"y": y, "x": x}, nil, snap, "/t")
	if f == nil || f.Reason != refusal.PluginFailed || f.Class != refusal.ClassAgentError ||
		!strings.Contains(f.Detail, "aaaa1111") || !strings.Contains(f.Detail, "x, y") {
		t.Fatalf("failure %+v", f)
	}
	if len(x.got)+len(y.got) != 0 {
		t.Error("a plugin was asked")
	}
}

func TestAPlanThatCannotBeLaidOutIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main")
	bad := sdk.ExtractPlan{Files: []sdk.ExtractFile{{SnapshotID: snap.ID, Path: "/a", Name: "../escape"}}}
	_, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": &extractor{plan: bad}}, nil, snap, "/t")
	if f == nil || f.Reason != refusal.PluginFailed || f.Class != refusal.ClassAgentError ||
		!strings.Contains(f.Detail, "x") || !strings.Contains(f.Detail, "../escape") || !strings.Contains(f.Detail, "nothing was written") {
		t.Fatalf("failure %+v", f)
	}
}

func TestWithoutALabelThePluginIsNamed(t *testing.T) {
	snap := tagged("aaaa1111", "x.part=main")
	plan := oneFile(snap.ID)
	plan.Label = ""
	got, f := restore.PlanExtraction(map[string]sdk.Extractor{"x": &extractor{plan: plan}}, nil, snap, "/t")
	if f != nil || got.Label != "x" {
		t.Errorf("%+v, %v", got, f)
	}
}
