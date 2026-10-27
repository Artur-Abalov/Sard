// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// streaming restores like a database: it lists the snapshots and dumps one
// file of them, and logs what it does.
type streaming struct {
	*fakePlugin
	listed  []sdk.SnapshotInfo
	secret  error
	dumpErr error
	// level is the level of its second line.
	level sdk.Level
}

func (p *streaming) RestoreInto(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	h.Phase(sdk.PhaseRestoring)
	h.Progress(1, 2)
	h.Log(sdk.LevelInfo, "restoring "+req.SnapshotID)
	h.Log(p.levelOf(), "info")
	_, p.secret = h.Secret("db")
	if err := h.Journal().Save([]byte("{}")); err != nil {
		return sdk.RestoredCopy{}, err
	}
	listed, err := h.Snapshots(ctx)
	if err != nil {
		return sdk.RestoredCopy{}, err
	}
	p.listed = listed
	p.dumpErr = h.Dump(ctx, req.SnapshotID, "/dump", &bytes.Buffer{})
	return sdk.RestoredCopy{Config: sdk.Config(`{"database":"copy"}`), Result: sdk.RestoreResult{Target: "database copy"}}, nil
}

// streamingRig: a plugin that tags its fingerprint on the main snapshot.
func streamingRig(t *testing.T) (*rig, *streaming) {
	r := newRig(t)
	r.store.snapshots[0].Tags = append(r.store.snapshots[0].Tags, "sard.fp="+r.step.GetVerify().GetFingerprintSha256())
	return r, &streaming{fakePlugin: r.plugin}
}

func (p *streaming) levelOf() sdk.Level {
	if p.level == 0 {
		return sdk.LevelWarn
	}
	return p.level
}

func TestAPluginRestoresACopyWithTheRepositoryOfTheStepAndNoSecret(t *testing.T) {
	r, p := streamingRig(t)

	result, err := newVerifier(r).Run(t.Context(), r.step, p, r.store, r.reporter)

	if err != nil || result.GetVerify().GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_PASSED {
		t.Fatalf("result %v, err %v", result, err)
	}
	requireSawTheRepositoryOnly(t, p)
	if got := r.reporter.logs; len(got) != 2 || got[0] != "restoring "+snapshotID || r.plugin.measured[0].Root != "" {
		t.Fatalf("logs %q, measured %+v", got, r.plugin.measured)
	}
}

// requireSawTheRepositoryOnly: the plugin listed and dumped the snapshots of
// the step and got no secret.
func requireSawTheRepositoryOnly(t *testing.T, p *streaming) {
	t.Helper()
	if len(p.listed) != 2 || p.listed[0].ID != snapshotID || p.dumpErr == nil || p.secret == nil {
		t.Fatalf("listed %v, dump %v, secret %v", p.listed, p.dumpErr, p.secret)
	}
}

func TestAPluginThatCannotListTheSnapshotsCannotRestore(t *testing.T) {
	r, p := streamingRig(t)
	calls := 0
	lister := &listOnce{fakeStore: r.store, calls: &calls}

	result, _ := newVerifier(r).Run(t.Context(), r.step, p, lister, r.reporter)

	if s := find(result.GetVerify(), "restore"); s.GetStatus() != cannotRun {
		t.Fatalf("stage %v", s)
	}
}

// listOnce lists for the verifier, then fails as a network would.
type listOnce struct {
	*fakeStore
	calls *int
}

func (l *listOnce) Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error) {
	*l.calls++
	if *l.calls > 1 {
		return nil, restic.ErrNetwork
	}
	return l.fakeStore.Snapshots(ctx, tags)
}
