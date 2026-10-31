// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/retention"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The PRUNE step with a fake restic that records every call (the @unit
// scenarios of docs/specs/agent/retention-prune.feature).

type pruneStore struct {
	calls    []string
	unlock   error
	summary  restic.PruneSummary
	pruneErr error
	// block makes prune wait for the end of its context.
	block bool
}

func (s *pruneStore) Unlock(context.Context) error {
	s.calls = append(s.calls, "unlock")
	return s.unlock
}

func (s *pruneStore) Prune(ctx context.Context) (restic.PruneSummary, error) {
	s.calls = append(s.calls, "prune")
	if s.block {
		<-ctx.Done()
		return restic.PruneSummary{}, context.Cause(ctx)
	}
	return s.summary, s.pruneErr
}

type pruneRig struct {
	t     *testing.T
	store *pruneStore
	log   *fakeReporter
}

func newPruneRig(t *testing.T) *pruneRig {
	return &pruneRig{t: t, store: &pruneStore{}, log: &fakeReporter{}}
}

func (r *pruneRig) handler() *retention.PruneHandler {
	return retention.NewPrune(retention.PruneOptions{
		Stores: func(name, _ string, _ io.Writer) (retention.PruneStore, bool) { return r.store, name == "R" },
	})
}

func pruneStep() *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", Action: agentv1.Action_ACTION_PRUNE, RepositoryName: "R", Prune: &agentv1.PruneTask{}}
}

func (r *pruneRig) run(step *agentv1.RunStep) (*agentv1.PruneOutput, error) {
	return r.runCtx(context.Background(), step)
}

func (r *pruneRig) runCtx(ctx context.Context, step *agentv1.RunStep) (*agentv1.PruneOutput, error) {
	r.t.Helper()
	res, err := r.handler().Run(ctx, step, r.log)
	return res.GetPrune(), err
}

func u64(v uint64) *uint64 { return &v }

var resticSummary = restic.PruneSummary{
	Freed: u64(1073741824), Size: u64(53687091200),
	Lines: []string{"total prune:        1000 blobs / 1.000 GiB", "remaining:         50000 blobs / 50.000 GiB"},
}

func TestВызовыResticШагаPRUNEТолькоUnlockИPrune(t *testing.T) {
	r := newPruneRig(t)
	if _, err := r.run(pruneStep()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.store.calls, []string{"unlock", "prune"}) {
		t.Errorf("calls = %v", r.store.calls)
	}
}

func TestИтогPRUNEНесётОсвобождённоеМестоИРазмерИзСводкиRestic(t *testing.T) {
	r := newPruneRig(t)
	r.store.summary = resticSummary
	out, err := r.run(pruneStep())
	if err != nil || out.FreedBytes == nil || *out.FreedBytes != 1073741824 || out.RepositorySizeBytes == nil || *out.RepositorySizeBytes != 53687091200 {
		t.Errorf("output %v, err %v", out, err)
	}
	if out.Failure != agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED {
		t.Errorf("failure = %v", out.Failure)
	}
}

func TestНеразобраннаяСводкаНеПревращаетУспехВПровал(t *testing.T) {
	r := newPruneRig(t)
	r.store.summary = restic.PruneSummary{Lines: []string{"done"}}
	out, err := r.run(pruneStep())
	if err != nil || out == nil || out.FreedBytes != nil || out.RepositorySizeBytes != nil {
		t.Errorf("output %v, err %v", out, err)
	}
}

func TestЛогШагаPRUNEНазываетОсвобождённоеМесто(t *testing.T) {
	r := newPruneRig(t)
	r.store.summary = resticSummary
	if _, err := r.run(pruneStep()); err != nil {
		t.Fatal(err)
	}
	for _, want := range append(slices.Clone(resticSummary.Lines), "prune freed 1073741824 bytes, repository size 53687091200 bytes") {
		if !slices.Contains(r.log.lines, want) {
			t.Errorf("log %q lacks %q", r.log.lines, want)
		}
	}
}

func TestЛогШагаPRUNEБезСводкиНеНазываетЧисел(t *testing.T) {
	r := newPruneRig(t)
	r.store.summary = restic.PruneSummary{Lines: []string{"done"}}
	if _, err := r.run(pruneStep()); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(r.log.lines, func(l string) bool { return strings.Contains(l, "freed") }) {
		t.Errorf("log = %q", r.log.lines)
	}
}

func TestШагPRUNEСЧемЛибоВнеКонтрактаОтклоняетсяБезОбращенияКХранилищу(t *testing.T) {
	for name, mutate := range map[string]func(*agentv1.RunStep){
		"plugin":                 func(s *agentv1.RunStep) { s.Plugin = "files" },
		"config_json max_unused": func(s *agentv1.RunStep) { s.ConfigJson = `{"max_unused":"100%"}` },
		"config_json unsafe":     func(s *agentv1.RunStep) { s.ConfigJson = `{"unsafe":true}` },
		"tags":                   func(s *agentv1.RunStep) { s.Tags = map[string]string{"retention_floor": "disabled"} },
		"snapshot_id":            func(s *agentv1.RunStep) { s.SnapshotId = "a1b2c3d4" },
		"ForgetTask":             func(s *agentv1.RunStep) { s.Forget = keepLast(1) },
		"VerifyTask":             func(s *agentv1.RunStep) { s.Verify = &agentv1.VerifyTask{JobId: "j1"} },
		"неизвестное поле в PruneTask": func(s *agentv1.RunStep) { s.Prune.ProtoReflect().SetUnknown(unknownField99) },
		"неизвестное поле в RunStep":   func(s *agentv1.RunStep) { s.ProtoReflect().SetUnknown(unknownField99) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newPruneRig(t)
			step := pruneStep()
			mutate(step)
			r.rejected(step, "prune accepts no overrides")
		})
	}
}

func (r *pruneRig) rejected(step *agentv1.RunStep, message string) {
	r.t.Helper()
	_, err := r.run(step)
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), message) {
		r.t.Errorf("err = %v, want a rejection with %q", err, message)
	}
	if len(r.store.calls) != 0 {
		r.t.Errorf("the repository was touched: %v", r.store.calls)
	}
}

func TestНедопустимыйШагPRUNEОтклоняетсяДоОбращенияКХранилищу(t *testing.T) {
	for name, c := range map[string]struct {
		mutate  func(*agentv1.RunStep)
		message string
	}{
		"без PruneTask":                {func(s *agentv1.RunStep) { s.Prune = nil }, "prune task missing"},
		"пустой repository_name":       {func(s *agentv1.RunStep) { s.RepositoryName = "" }, "unknown repository"},
		"repository_name которого нет": {func(s *agentv1.RunStep) { s.RepositoryName = "other" }, `unknown repository "other"`},
	} {
		t.Run(name, func(t *testing.T) {
			r := newPruneRig(t)
			step := pruneStep()
			c.mutate(step)
			r.rejected(step, c.message)
		})
	}
}

func (r *pruneRig) failed(err error, failure agentv1.MaintenanceFailure) *agentv1.PruneOutput {
	r.t.Helper()
	out, got := r.run(pruneStep())
	if got == nil || errors.Is(got, executor.ErrRejected) || out == nil || out.Failure != failure {
		r.t.Fatalf("output %v, err %v, want failure %v", out, got, failure)
	}
	return out
}

func TestЖиваяБлокировкаДаётИтогPRUNEРепозиторийЗанятСДержателем(t *testing.T) {
	r := newPruneRig(t)
	r.store.pruneErr = fmt.Errorf("restic prune: %w", &restic.LockedError{Holder: "PID 4242 on db2 by sard-agent (UID 0, GID 0)"})
	out := r.failed(r.store.pruneErr, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED)
	if !strings.Contains(out.LockHolder, "PID 4242") || !strings.Contains(out.LockHolder, "db2") {
		t.Errorf("holder = %q", out.LockHolder)
	}
}

func TestУчёткаБезПраваУдаленияДаётТотЖеОтказЧтоУFORGETДляPRUNE(t *testing.T) {
	r := newPruneRig(t)
	r.store.pruneErr = fmt.Errorf("restic prune: %w", &restic.ExitError{Code: 1, Message: "Fatal: Remove(<data/ab>) returned error: AccessDenied: Access Denied."})
	r.failed(r.store.pruneErr, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_DELETE_DENIED)
	_, err := r.run(pruneStep())
	const want = "storage account has no delete permission: assign repository maintenance to a host whose account can delete"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v", err)
	}
}

func TestНедоступноеХранилищеДаётПричинуХранилищеНедоступноДляPRUNE(t *testing.T) {
	r := newPruneRig(t)
	r.store.pruneErr = fmt.Errorf("%w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: connection refused"})
	r.failed(r.store.pruneErr, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE)
}

func TestСбойUnlockПередPRUNEНеЗапускаетPrune(t *testing.T) {
	r := newPruneRig(t)
	r.store.unlock = fmt.Errorf("%w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: no such host"})
	r.failed(r.store.unlock, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE)
	if !slices.Equal(r.store.calls, []string{"unlock"}) {
		t.Errorf("calls = %v", r.store.calls)
	}
}

func TestНеизвестныйСбойPRUNEДаётПровалСТекстомRestic(t *testing.T) {
	r := newPruneRig(t)
	r.store.pruneErr = fmt.Errorf("restic prune: %w", &restic.ExitError{Code: 1, Message: "Fatal: unexpected thing"})
	r.failed(r.store.pruneErr, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED)
	_, err := r.run(pruneStep())
	if err == nil || !strings.Contains(err.Error(), "Fatal: unexpected thing") {
		t.Errorf("err = %v", err)
	}
}

func TestОтменаПосредиPRUNEПрерываетResticИНеДаётКодаСбоя(t *testing.T) {
	r := newPruneRig(t)
	r.store.block = true
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("cancelled by the server"))
	out, err := r.runCtx(ctx, pruneStep())
	if err == nil || out.GetFailure() != agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED {
		t.Errorf("output %v, err %v", out, err)
	}
	if !slices.Equal(r.store.calls, []string{"unlock", "prune"}) {
		t.Errorf("calls = %v", r.store.calls)
	}
}

func TestШагFORGETПоПрежнемуВыполняетсяРядомСPRUNE(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 5))
	if out := r.mustRun(forgetStep(keepLast(1))); len(out.ForgottenSnapshotIds) != 4 {
		t.Errorf("forgotten = %v", out.ForgottenSnapshotIds)
	}
}
