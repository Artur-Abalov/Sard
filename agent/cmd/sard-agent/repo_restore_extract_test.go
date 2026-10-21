// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Rule "repo restore выгружает снимок плагина файлами по его плану, не зная базы"
// (R2c-3, РВ-8, О2, О3, В10): the plugin X of the tests plans, the agent lays out.

const idX = "eeee1111" + "00000000000000000000000000000000000000000000000000000000"

// extractPlugin is a plugin of the tests that plans the files of a snapshot.
type extractPlugin struct {
	stubPlugin
	plan  func(sdk.ExtractRequest) (sdk.ExtractPlan, error)
	asked *[]sdk.ExtractRequest
}

func (p extractPlugin) PlanExtract(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	*p.asked = append(*p.asked, req)
	return p.plan(req)
}

// outBin is the plan of the scenarios: out.bin from the path /a of the snapshot, and a hint.
func outBin(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	return sdk.ExtractPlan{Label: "X", Files: []sdk.ExtractFile{{SnapshotID: req.Snapshot.ID, Path: "/a", Name: "out.bin"}},
		Hints: []string{"run x-load " + req.Target + "/out.bin"}}, nil
}

// withExtractor registers the plugin x and a snapshot X tagged x.part=main that holds /a.
func (h *setupHost) withExtractor(plan func(sdk.ExtractRequest) (sdk.ExtractPlan, error)) *[]sdk.ExtractRequest {
	h.t.Helper()
	asked := new([]sdk.ExtractRequest)
	h.withPlugin(extractPlugin{stubPlugin: stubPlugin{"x"}, plan: plan, asked: asked})
	h.base().snapshots = append(h.base().snapshots, restic.Snapshot{ID: idX, Time: time.Date(2026, 10, 12, 9, 0, 0, 0, msk), Hostname: "db1",
		Paths: []string{"/a"}, Tags: []string{"sard.source=s3", "x.part=main"}})
	h.base().restoreState.files[idX] = []snapFile{{"/a", "X-BYTES"}}
	return asked
}

// Scenario: Снимок плагина с выгрузкой выкладывается хостом файлами по плану плагина.
func TestASnapshotOfAPluginIsLaidOutAsTheFilesOfItsPlan(t *testing.T) {
	h := restoreHost(t)
	asked := h.withExtractor(outBin)
	code, stdout, stderr := h.restore(idX)
	assertCode(t, code, exitOK)
	if stderr != "" || len(h.restoreCalls()) != 0 {
		t.Errorf("stderr %q, restore calls %v", stderr, h.restoreCalls())
	}
	if got := h.names(h.target()); !slices.Equal(got, []string{"out.bin"}) {
		t.Fatalf("target holds %v", got)
	}
	h.assertPGFile("out.bin", "X-BYTES")
	if !strings.Contains(stdout, "run x-load "+h.target()+"/out.bin") || !strings.Contains(stdout, "file:     "+h.target()+"/out.bin") {
		t.Errorf("stdout:\n%s", stdout)
	}
	assertAskedOnce(t, *asked, h.target())
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "restored to "+h.target()) {
		t.Errorf("audit %q", h.log.lines)
	}
}

// assertAskedOnce: the plugin was asked once, for the snapshot X and the target.
func assertAskedOnce(t *testing.T, asked []sdk.ExtractRequest, target string) {
	t.Helper()
	if len(asked) != 1 || asked[0].Target != target || asked[0].Snapshot.ID != idX {
		t.Errorf("the plugin was asked %+v", asked)
	}
}

// Scenario: Снимок без метки плагина с выгрузкой восстанавливается деревом.
func TestASnapshotWithoutATagOfAPluginIsRestoredAsATreeAndNoPluginIsAsked(t *testing.T) {
	h := restoreHost(t)
	asked := h.withExtractor(outBin)
	code, _, _ := h.restore(idF)
	assertCode(t, code, exitOK)
	h.assertRestored(idF)
	if len(*asked) != 0 {
		t.Errorf("the plugin was asked %+v", *asked)
	}
}

func TestAPlanWithoutFilesRestoresTheSnapshotAsATree(t *testing.T) {
	h := restoreHost(t)
	h.withExtractor(func(sdk.ExtractRequest) (sdk.ExtractPlan, error) { return sdk.ExtractPlan{}, nil })
	code, _, _ := h.restore(idX)
	assertCode(t, code, exitOK)
	h.assertRestored(idX)
}

// Scenario: latest пропускает дополнительный снимок любого плагина.
func TestLatestSkipsTheExtraSnapshotOfAnyPlugin(t *testing.T) {
	h := restoreHost(t)
	h.withExtractor(outBin)
	extra := "ffff1111" + strings.Repeat("0", 56)
	h.base().snapshots = append(h.base().snapshots, restic.Snapshot{ID: extra, Time: time.Date(2026, 10, 12, 12, 0, 0, 0, msk), Hostname: "db1",
		Paths: []string{"/e"}, Tags: []string{"sard.source=s3", "x.main_snapshot=" + idX}})
	code, _, _ := h.restore("latest", "--source", "s3")
	assertCode(t, code, exitOK)
	if len(h.dumpCalls()) != 1 || h.dumpCalls()[0].args[len(h.dumpCalls()[0].args)-2] != idX {
		t.Errorf("dump calls %v", h.dumpCalls())
	}
}

// Scenario: Ошибка плана выгрузки ничего не записывает (В10).
func TestAnErrorOfThePlanWritesNothingAndIsTheAgentsFault(t *testing.T) {
	h := restoreHost(t)
	h.withExtractor(func(sdk.ExtractRequest) (sdk.ExtractPlan, error) {
		return sdk.ExtractPlan{}, errors.New("no main file")
	})
	code, _, stderr := h.restore(idX)
	assertRefusal(t, code, stderr, exitAgentError, "RESTIC_OUTPUT_UNEXPECTED")
	if !strings.Contains(stderr, "no main file") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertNothingWritten("plan error")
}

func TestASnapshotOfTwoPluginsIsRefusedWithoutAnyWrite(t *testing.T) {
	h := restoreHost(t)
	asked := h.withExtractor(outBin)
	h.withPlugin(extractPlugin{stubPlugin: stubPlugin{"x"}, plan: outBin, asked: asked}, extractPlugin{stubPlugin: stubPlugin{"y"}, plan: outBin, asked: asked})
	for i := range h.base().snapshots {
		if h.base().snapshots[i].ID == idX {
			h.base().snapshots[i].Tags = append(h.base().snapshots[i].Tags, "y.part=main")
		}
	}
	code, _, stderr := h.restore(idX)
	assertRefusal(t, code, stderr, exitAgentError, "PLUGIN_FAILED")
	if !strings.Contains(stderr, "x, y") || len(*asked) != 0 {
		t.Errorf("stderr %q, asked %v", stderr, *asked)
	}
	h.assertNothingWritten("two plugins")
}

func TestPathWithASnapshotOfAPluginIsAUsageErrorThatNamesWhatItIs(t *testing.T) {
	h := restoreHost(t)
	h.withExtractor(outBin)
	code, _, stderr := h.restore(idX, "--path", "/a")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "--path") || !strings.Contains(stderr, "X snapshot") {
		t.Errorf("stderr %q", stderr)
	}
	h.assertAbsent(h.target())
	if len(h.dumpCalls()) != 0 || len(h.log.lines) != 0 {
		t.Errorf("dump %v, audit %v", h.dumpCalls(), h.log.lines)
	}
}

func TestAPlanWithAnUnsafeNameWritesNothing(t *testing.T) {
	h := restoreHost(t)
	h.withExtractor(func(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
		return sdk.ExtractPlan{Files: []sdk.ExtractFile{{SnapshotID: req.Snapshot.ID, Path: "/a", Name: "../escape"}}}, nil
	})
	code, _, stderr := h.restore(idX)
	assertRefusal(t, code, stderr, exitAgentError, "PLUGIN_FAILED")
	h.assertNothingWritten("unsafe name")
}
