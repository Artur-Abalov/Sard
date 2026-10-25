// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// copier is a plugin a verifier can verify: it restores and measures a copy.
type copier struct{ plugin }

func (*copier) Fingerprint(context.Context, sdk.Host, sdk.Config, sdk.FingerprintTarget) (sdk.Fingerprint, error) {
	return sdk.Fingerprint{}, nil
}

func (*copier) RestoreInto(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoredCopy, error) {
	root, err := h.RestoreTree(ctx, req.SnapshotID)
	return sdk.RestoredCopy{Root: root}, err
}

// readOnly is a repository a verifier reads: the fake with check and a verified restore.
type readOnly struct {
	*repo
	checked  []int
	restored []string
}

func (r *readOnly) VerifyData(_ context.Context, percent int) (restic.CheckReport, error) {
	r.checked = append(r.checked, percent)
	return restic.CheckReport{}, nil
}

func (r *readOnly) RestoreVerified(_ context.Context, id, target string) error {
	r.restored = append(r.restored, id+" "+target)
	return nil
}

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 10, 25, 4, 30, 0, 0, time.UTC) }

func (clock) AfterFunc(time.Duration, func()) executor.Timer { return time.NewTimer(time.Hour) }

const verifiedSnapshot = "6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc6c719cbc"

func newVerifier(t *testing.T) *verifier.Verifier {
	noexec := func(_ string, st *syscall.Statfs_t) error {
		st.Flags, st.Bavail, st.Bsize = 0x8, 1<<30, 4096
		return nil
	}
	return verifier.New(verifier.Options{
		WorkDir: t.TempDir(), MachineID: strings.Repeat("1", 64), CheckPercent: 10, Clock: clock{},
		Disk:   verifier.OSDisk{Statfs: noexec},
		Limits: verifier.Limits{Prepare: time.Hour, Check: time.Hour, Restore: time.Hour, Fingerprint: time.Hour},
		Space:  verifier.Space{Factor: 2},
	})
}

func verifyStep() *agentv1.RunStep {
	s := step(verify, `{}`)
	s.SnapshotId = verifiedSnapshot
	s.Tags = map[string]string{"sard.source": "s1"}
	s.Verify = &agentv1.VerifyTask{JobId: "job-1", Deadline: timestamppb.New(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))}
	return s
}

func TestOnlyAVerifierHostAnnouncesVerifyAndOnlyForAPluginThatRestoresACopy(t *testing.T) {
	with := []pluginhost.Option{pluginhost.WithVerifier(newVerifier(t))}
	cases := []struct {
		f    *handlersFixture
		want []agentv1.Action
	}{
		{newHandlers(t, &copier{}), []agentv1.Action{backup}},
		{newHandlersWith(t, with, &copier{}), []agentv1.Action{backup, verify}},
		{newHandlersWith(t, with, &plugin{}), []agentv1.Action{backup}},
	}
	for i, c := range cases {
		h, _ := c.f.handlers.Handler("fake")
		if got := h.Actions(); !slices.Equal(got, c.want) {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestAVerifyStepRunsThePipelineOverTheRepositoryOfTheStep(t *testing.T) {
	f := newHandlersWith(t, []pluginhost.Option{pluginhost.WithVerifier(newVerifier(t))}, &copier{})
	store := &readOnly{repo: &repo{snapshots: []restic.Snapshot{{ID: verifiedSnapshot}}}}
	f.readOnly = store

	result, err := f.run(t, verifyStep())

	v := result.GetVerify()
	// Without a fingerprint the copy is restored and cannot be compared.
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY || err == nil || !strings.Contains(err.Error(), "no fingerprint") {
		t.Fatalf("result %v, err %v", result, err)
	}
	if !slices.Equal(store.checked, []int{10}) || len(store.restored) != 1 || !strings.HasPrefix(store.restored[0], verifiedSnapshot+" ") {
		t.Fatalf("checked %v, restored %v", store.checked, store.restored)
	}
}

func TestAVerifyStepOverARepositoryAVerifierCannotReadIsRejected(t *testing.T) {
	f := newHandlersWith(t, []pluginhost.Option{pluginhost.WithVerifier(newVerifier(t))}, &copier{})

	result, err := f.run(t, verifyStep())

	if !errors.Is(err, executor.ErrRejected) || result != nil || !strings.Contains(err.Error(), `repository "main" cannot be read by a verifier`) {
		t.Fatalf("result %v, err %v", result, err)
	}
}

func TestAVerifyStepOnAHostWithoutAVerifierIsRejected(t *testing.T) {
	f := newHandlers(t, &copier{})

	if _, err := f.run(t, verifyStep()); !errors.Is(err, executor.ErrRejected) {
		t.Fatalf("err %v", err)
	}
}
