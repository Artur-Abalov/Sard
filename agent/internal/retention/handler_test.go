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
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/retention"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/durationpb"
)

// The FORGET step with a fake restic that holds a list of snapshots and
// records every call (the @unit scenarios of docs/specs/agent/
// retention-forget.feature).

type fakeStore struct {
	snaps []restic.Snapshot
	calls []string // "snapshots", "unlock", "forget"
	// forgets are the ids of each restic forget.
	forgets [][]string
	// listErr, forgetErr and unlockErr are asked with the number of the call.
	listErr   func(n int) error
	forgetErr func(n int) error
	unlockErr error
	// afterForget runs after the nth forget removed its snapshots.
	afterForget func(n int)
	badList     bool
}

func (s *fakeStore) Snapshots(_ context.Context, tags []string) ([]restic.Snapshot, error) {
	s.calls = append(s.calls, "snapshots")
	if s.listErr != nil {
		if err := s.listErr(s.count("snapshots")); err != nil {
			return nil, err
		}
	}
	if s.badList {
		return nil, fmt.Errorf("restic snapshots: %w: invalid JSON", restic.ErrBadOutput)
	}
	var out []restic.Snapshot
	for _, sn := range s.snaps {
		if slices.ContainsFunc(tags, func(t string) bool { return !slices.Contains(sn.Tags, t) }) {
			continue
		}
		out = append(out, sn)
	}
	return out, nil
}

func (s *fakeStore) Unlock(context.Context) error {
	s.calls = append(s.calls, "unlock")
	return s.unlockErr
}

func (s *fakeStore) Forget(_ context.Context, ids []string) error {
	s.calls = append(s.calls, "forget")
	s.forgets = append(s.forgets, ids)
	n := len(s.forgets)
	if s.forgetErr != nil {
		if err := s.forgetErr(n); err != nil {
			return err
		}
	}
	s.snaps = slices.DeleteFunc(s.snaps, func(sn restic.Snapshot) bool { return slices.Contains(ids, sn.ID) })
	if s.afterForget != nil {
		s.afterForget(n)
	}
	return nil
}

func (s *fakeStore) count(call string) int {
	n := 0
	for _, c := range s.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (s *fakeStore) untouched() bool { return len(s.calls) == 0 }

func (s *fakeStore) forgotten() []string { return slices.Concat(s.forgets...) }

type fakeReporter struct {
	executor.Reporter
	lines []string
}

func (r *fakeReporter) Log(_ agentv1.LogLevel, text string) { r.lines = append(r.lines, text) }
func (r *fakeReporter) Output() io.Writer                   { return io.Discard }

type rig struct {
	store *fakeStore
	floor config.Floor
	log   *fakeReporter
	t     *testing.T
}

func newRig(t *testing.T, floor config.Floor, snaps ...[]restic.Snapshot) *rig {
	return &rig{t: t, floor: floor, log: &fakeReporter{}, store: &fakeStore{snaps: slices.Concat(snaps...)}}
}

func (r *rig) handler() *retention.Handler {
	return retention.New(retention.Options{
		Stores: func(name, _ string, _ io.Writer) (retention.Store, bool) { return r.store, name == "R" },
		Floors: func(name string) config.Floor { return r.floor },
		Now:    func() time.Time { return now },
	})
}

func forgetStep(task *agentv1.ForgetTask) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", Action: agentv1.Action_ACTION_FORGET, RepositoryName: "R", Forget: task}
}

func keepLast(n uint32) *agentv1.ForgetTask {
	return &agentv1.ForgetTask{SourceId: "S", Policy: &agentv1.RetentionPolicy{KeepLast: n}}
}

func (r *rig) run(step *agentv1.RunStep) (*agentv1.ForgetOutput, error) {
	return r.runCtx(context.Background(), step)
}

func (r *rig) runCtx(ctx context.Context, step *agentv1.RunStep) (*agentv1.ForgetOutput, error) {
	r.t.Helper()
	res, err := r.handler().Run(ctx, step, r.log)
	return res.GetForget(), err
}

func (r *rig) mustRun(step *agentv1.RunStep) *agentv1.ForgetOutput {
	r.t.Helper()
	out, err := r.run(step)
	if err != nil {
		r.t.Fatalf("step failed: %v", err)
	}
	return out
}

func (r *rig) rejected(step *agentv1.RunStep, message string) {
	r.t.Helper()
	_, err := r.run(step)
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), message) {
		r.t.Errorf("err = %v, want a rejection with %q", err, message)
	}
	if !r.store.untouched() {
		r.t.Errorf("the repository was touched: %v", r.store.calls)
	}
}

func TestХранилищеНеТронутоПриНедопустимомЗадании(t *testing.T) {
	for name, step := range map[string]*agentv1.RunStep{
		"без ForgetTask":      forgetStep(nil),
		"пустой source_id":    forgetStep(&agentv1.ForgetTask{Policy: &agentv1.RetentionPolicy{KeepLast: 1}}),
		"source_id с запятой": forgetStep(&agentv1.ForgetTask{SourceId: "s1,sard.run=x", Policy: &agentv1.RetentionPolicy{KeepLast: 1}}),
		"отрицательный keep_within": forgetStep(&agentv1.ForgetTask{SourceId: "S",
			Policy: &agentv1.RetentionPolicy{KeepLast: 1, KeepWithin: durationpb.New(-time.Hour)}}),
		"неполные без срока": forgetStep(&agentv1.ForgetTask{SourceId: "S", Policy: &agentv1.RetentionPolicy{KeepLast: 1},
			IncompleteSnapshotIds: ids("m1")}),
		"отрицательный срок неполных": forgetStep(&agentv1.ForgetTask{SourceId: "S", Policy: &agentv1.RetentionPolicy{KeepLast: 1},
			IncompleteSnapshotIds: ids("m1"), IncompleteMaxAge: durationpb.New(-time.Hour)}),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, noFloor, daily("S", 5))
			r.rejected(step, "")
		})
	}
	t.Run("репозитория нет на хосте", func(t *testing.T) {
		r := newRig(t, noFloor, daily("S", 5))
		step := forgetStep(keepLast(1))
		step.RepositoryName = "nope"
		r.rejected(step, "unknown repository")
	})
}

func TestПустаяПолитикаОтклоняетсяИНичегоНеЗабывает(t *testing.T) {
	r := newRig(t, builtInFloor, daily("S", 30))
	r.rejected(forgetStep(&agentv1.ForgetTask{SourceId: "S", Policy: &agentv1.RetentionPolicy{KeepWithin: durationpb.New(0)}}), "retention policy is empty")
	r.rejected(forgetStep(&agentv1.ForgetTask{SourceId: "S"}), "retention policy is empty")
}

func TestШагFORGETСИдентификаторомСнимкаОтклоняетсяБезОбращенияКХранилищу(t *testing.T) {
	r := newRig(t, builtInFloor, daily("S", 30))
	step := forgetStep(keepLast(1))
	step.SnapshotId = id("m30")
	r.rejected(step, "forget takes no snapshot id")
}

// unknownField99 is the field 99 with the value 1, which a newer server may send.
var unknownField99 = protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)

func TestШагFORGETСЧемЛибоВнеКонтрактаОтклоняетсяИПределНеОбходится(t *testing.T) {
	for name, mutate := range map[string]func(*agentv1.RunStep){
		"plugin":                 func(s *agentv1.RunStep) { s.Plugin = "files" },
		"config_json force":      func(s *agentv1.RunStep) { s.ConfigJson = `{"force":true}` },
		"config_json remove_all": func(s *agentv1.RunStep) { s.ConfigJson = `{"remove_all":true}` },
		"tags":                   func(s *agentv1.RunStep) { s.Tags = map[string]string{"retention_floor": "disabled"} },
		"verify":                 func(s *agentv1.RunStep) { s.Verify = &agentv1.VerifyTask{} },
		"неизвестное поле в шаге": func(s *agentv1.RunStep) { s.ProtoReflect().SetUnknown(unknownField99) },
		"неизвестное поле в ForgetTask": func(s *agentv1.RunStep) {
			s.Forget.ProtoReflect().SetUnknown(unknownField99)
		},
		"неизвестное поле в RetentionPolicy": func(s *agentv1.RunStep) {
			s.Forget.Policy.ProtoReflect().SetUnknown(unknownField99)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, builtInFloor, daily("S", 30))
			step := forgetStep(keepLast(1))
			mutate(step)
			r.rejected(step, "forget accepts no overrides")
		})
	}
}

func TestИсточникБезСнимковЗавершаетсяУспехомБезУдаления(t *testing.T) {
	r := newRig(t, noFloor)
	out := r.mustRun(forgetStep(keepLast(1)))
	if len(out.ForgottenSnapshotIds)+len(out.RemainingSnapshotIds) != 0 || out.ListedAt == nil || r.store.count("forget") != 0 {
		t.Errorf("output %v, calls %v", out, r.store.calls)
	}
}

func TestПолитикаОставляющаяВсёНеВызываетУдаления(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 5))
	out := r.mustRun(forgetStep(keepLast(4294967295)))
	if len(out.ForgottenSnapshotIds) != 0 || r.store.count("forget") != 0 || r.store.count("unlock") != 0 {
		t.Errorf("output %v, calls %v", out, r.store.calls)
	}
	if len(out.RemainingSnapshotIds) != 5 {
		t.Errorf("remaining %d", len(out.RemainingSnapshotIds))
	}
}

func TestУспешныйForgetСообщаетЗабытыеИОставшиесяСнимкиИВремяПеречисления(t *testing.T) {
	snaps := []restic.Snapshot{
		main("m1", "S", at(28, 10)), part("f1", "S", "m1", at(28, 10)),
		main("m2", "S", at(29, 10)), part("f2", "S", "m2", at(29, 10)),
		main("m3", "S", at(30, 10)), part("f3", "S", "m3", at(30, 10)),
	}
	r := newRig(t, noFloor, snaps)
	out := r.mustRun(forgetStep(keepLast(1)))
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, ids("m1", "f1", "m2", "f2"))
	sameSet(t, "remaining", out.RemainingSnapshotIds, ids("m3", "f3"))
	if !out.ListedAt.AsTime().Equal(now) || out.Failure != agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED {
		t.Errorf("output %v", out)
	}
	if len(r.store.forgets) != 2 {
		t.Fatalf("restic forget ran %d times, want one for the parts and one for the main snapshots", len(r.store.forgets))
	}
	sameSet(t, "first call", r.store.forgets[0], ids("f1", "f2"))
	sameSet(t, "second call", r.store.forgets[1], ids("m1", "m2"))
}

func TestУстаревшиеБлокировкиСнимаютсяДоУдаленияБезRemoveAll(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 30))
	r.mustRun(forgetStep(keepLast(1)))
	want := []string{"snapshots", "unlock", "forget", "snapshots"}
	if !slices.Equal(r.store.calls, want) {
		t.Errorf("calls %v, want %v", r.store.calls, want)
	}
}

func TestПредельПоказываетсяВЛогеШагаСКороткимиId(t *testing.T) {
	r := newRig(t, builtInFloor, weekly("S", 10))
	out := r.mustRun(forgetStep(keepLast(1)))
	log := strings.Join(r.log.lines, "\n")
	for _, id := range out.ForgottenSnapshotIds {
		if !strings.Contains(log, "forgetting "+id[:8]) {
			t.Errorf("the log does not name %s:\n%s", id[:8], log)
		}
	}
	for _, id := range out.KeptByFloorSnapshotIds {
		if !strings.Contains(log, "kept by host retention floor: "+id[:8]) {
			t.Errorf("the log does not name %s:\n%s", id[:8], log)
		}
	}
	if !strings.Contains(log, "keep_last=7 keep_within_days=14") || len(out.KeptByFloorSnapshotIds) == 0 {
		t.Errorf("log:\n%s", log)
	}
}

func TestВыключенныйПределНазванВЛоге(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 3))
	r.mustRun(forgetStep(keepLast(1)))
	if !strings.Contains(strings.Join(r.log.lines, "\n"), "floor is disabled") {
		t.Errorf("log %q", r.log.lines)
	}
}

func TestResticForgetПолучаетТолькоПолныеIdИзЗабытых(t *testing.T) {
	var snaps []restic.Snapshot
	for _, m := range weekly("S", 20) {
		name := strings.TrimSpace(m.ID)
		snaps = append(snaps, m, part("f"+name, "S", name, m.Time))
	}
	r := newRig(t, builtInFloor, snaps)
	task := &agentv1.ForgetTask{SourceId: "S", Policy: &agentv1.RetentionPolicy{KeepDaily: 1, KeepWeekly: 1, KeepWithin: durationpb.New(24 * time.Hour)}}
	out := r.mustRun(forgetStep(task))
	sameSet(t, "ids of restic forget", r.store.forgotten(), out.ForgottenSnapshotIds)
	for _, id := range r.store.forgotten() {
		if len(id) != 64 {
			t.Errorf("id %q is not a full id", id)
		}
	}
}

func TestДесятьТысячСнимковЗабываютсяОдНИМШагомНеДлинныеВызовы(t *testing.T) {
	var snaps []restic.Snapshot
	for i := 0; i < 10001; i++ {
		snaps = append(snaps, main(fmt.Sprintf("m%05d", i), "S", at(1, 0).Add(time.Duration(i)*time.Minute)))
	}
	r := newRig(t, noFloor, snaps)
	out := r.mustRun(forgetStep(keepLast(1)))
	if len(out.ForgottenSnapshotIds) != 10000 {
		t.Errorf("forgotten %d", len(out.ForgottenSnapshotIds))
	}
	for _, call := range r.store.forgets {
		if len(call)*65 > 128<<10 {
			t.Errorf("a call with %d ids is %d bytes long", len(call), len(call)*65)
		}
	}
}

func hasFailure(t *testing.T, out *agentv1.ForgetOutput, err error, want agentv1.MaintenanceFailure) {
	t.Helper()
	if err == nil || out.GetFailure() != want {
		t.Errorf("err = %v, failure = %v, want %v", err, out.GetFailure(), want)
	}
}

func TestСбойХранилищаПосредиУдаленияСообщаетТолькоРеальноУдалённыеСнимки(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	unavailable := fmt.Errorf("restic forget: %w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: connection refused"})
	// the storage forgets two snapshots of the call and then goes away
	failing := &partialStore{fakeStore: r.store, after: 2, err: unavailable}
	h := retention.New(retention.Options{Stores: func(string, string, io.Writer) (retention.Store, bool) { return failing, true },
		Floors: func(string) config.Floor { return noFloor }, Now: func() time.Time { return now }})
	res, err := h.Run(context.Background(), forgetStep(keepLast(1)), r.log)
	out := res.GetForget()
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE)
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, ids("m1", "m2"))
	if len(out.RemainingSnapshotIds) != 8 {
		t.Errorf("remaining %d", len(out.RemainingSnapshotIds))
	}
}

// partialStore forgets the first `after` ids of a call and then fails, as a
// storage that goes away in the middle of a restic forget.
type partialStore struct {
	*fakeStore
	after int
	err   error
}

func (p *partialStore) Forget(ctx context.Context, ids []string) error {
	if len(ids) > p.after {
		_ = p.fakeStore.Forget(ctx, ids[:p.after])
		return p.err
	}
	return p.fakeStore.Forget(ctx, ids)
}

func TestСбойХранилищаИПриПовторномПеречисленииНеДаётИтогаСЗабытымиСнимками(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	unavailable := fmt.Errorf("%w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: connection refused"})
	r.store.forgetErr = func(int) error { return unavailable }
	r.store.listErr = func(n int) error {
		if n > 1 {
			return unavailable
		}
		return nil
	}
	out, err := r.run(forgetStep(keepLast(1)))
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE)
	if len(out.ForgottenSnapshotIds)+len(out.RemainingSnapshotIds) != 0 || out.ListedAt != nil {
		t.Errorf("output %v", out)
	}
}

func TestНедоступноеХранилищеДоПеречисленияНичегоНеУдаляет(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	r.store.listErr = func(int) error {
		return fmt.Errorf("%w: %w", restic.ErrNetwork, &restic.ExitError{Code: 1, Message: "Fatal: no such host"})
	}
	out, err := r.run(forgetStep(keepLast(1)))
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE)
	if out.ListedAt != nil || len(out.RemainingSnapshotIds)+len(out.ForgottenSnapshotIds)+len(out.KeptByFloorSnapshotIds) != 0 {
		t.Errorf("output %v", out)
	}
	if r.store.count("forget") != 0 {
		t.Errorf("calls %v", r.store.calls)
	}
}

func TestУчёткаБезПраваУдаленияДаётЧестныйОтказСПричиной(t *testing.T) {
	r := newRig(t, builtInFloor, daily("S", 30))
	r.store.forgetErr = func(int) error {
		return fmt.Errorf("restic forget: %w", &restic.ExitError{Code: 1, Message: "Fatal: Remove(<data/ab>) returned error: AccessDenied: Access Denied."})
	}
	out, err := r.run(forgetStep(keepLast(1)))
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_DELETE_DENIED)
	if !strings.Contains(err.Error(), "storage account has no delete permission") || len(out.ForgottenSnapshotIds) != 0 {
		t.Errorf("err = %v, output %v", err, out)
	}
}

func TestНеразбираемыйСписокСнимковОтResticНичегоНеУдаляет(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	r.store.badList = true
	_, err := r.run(forgetStep(keepLast(1)))
	if err == nil || errors.Is(err, executor.ErrRejected) || r.store.count("forget") != 0 {
		t.Errorf("err = %v, calls %v", err, r.store.calls)
	}
}

func TestЖиваяБлокировкаДаётИтогРепозиторийЗанятСДержателем(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	r.store.forgetErr = func(int) error {
		return fmt.Errorf("restic forget: %w", &restic.LockedError{Holder: "PID 4242 on db2 by sard-agent (UID 0, GID 0)"})
	}
	out, err := r.run(forgetStep(keepLast(1)))
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED)
	if !strings.Contains(out.LockHolder, "PID 4242") || !strings.Contains(out.LockHolder, "db2") || !strings.Contains(err.Error(), "PID 4242") {
		t.Errorf("holder %q, err %v", out.LockHolder, err)
	}
	if len(out.RemainingSnapshotIds) != 10 {
		t.Errorf("remaining %d", len(out.RemainingSnapshotIds))
	}
}

func TestЗаблокированныйРепозиторийБезНазванногоДержателя(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	r.store.forgetErr = func(int) error { return fmt.Errorf("restic forget: %w", restic.ErrLocked) }
	out, err := r.run(forgetStep(keepLast(1)))
	hasFailure(t, out, err, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED)
	if out.LockHolder != "" || !strings.Contains(err.Error(), "locked") {
		t.Errorf("holder %q, err %v", out.LockHolder, err)
	}
}

func TestОтказСнятияУстаревшихБлокировокЗавершаетШагБезУдаления(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	r.store.unlockErr = errors.New("unlock failed")
	_, err := r.run(forgetStep(keepLast(1)))
	if err == nil || r.store.count("forget") != 0 {
		t.Errorf("err = %v, calls %v", err, r.store.calls)
	}
}

func TestСбойПослеУдаленияЧастейНеОставляетЧастьБезОсновного(t *testing.T) {
	var snaps []restic.Snapshot
	for _, m := range daily("S", 2) {
		name := strings.TrimSpace(m.ID)
		snaps = append(snaps, m, part("f"+name, "S", name, m.Time))
	}
	r := newRig(t, noFloor, snaps)
	r.store.forgetErr = func(n int) error {
		if n == 2 {
			return errors.New("denied")
		}
		return nil
	}
	out, err := r.run(forgetStep(keepLast(1)))
	if err == nil {
		t.Fatal("no error")
	}
	exists := map[string]bool{}
	for _, id := range out.RemainingSnapshotIds {
		exists[id] = true
	}
	for _, s := range r.store.snaps {
		if of, isPart := partOf(s); isPart && !exists[of] {
			t.Errorf("part %s without its main snapshot", strings.TrimSpace(s.ID))
		}
	}
}

func partOf(s restic.Snapshot) (string, bool) {
	for _, tag := range s.Tags {
		if of, ok := strings.CutPrefix(tag, "files.main_snapshot="); ok {
			return of, true
		}
	}
	return "", false
}

func TestОтменаПосредиУдаленияСообщаетУжеЗабытыеСнимкиИНеВызываетForgetПослеОтмены(t *testing.T) {
	r := newRig(t, noFloor, daily("S", 10))
	ctx, cancel := context.WithCancelCause(context.Background())
	r.store.afterForget = func(int) { cancel(errors.New("cancelled by the server")) }
	// parts and main snapshots go in separate calls, so there are two
	var snaps []restic.Snapshot
	for _, m := range daily("S", 3) {
		name := strings.TrimSpace(m.ID)
		snaps = append(snaps, m, part("f"+name, "S", name, m.Time))
	}
	r.store.snaps = snaps
	out, err := r.runCtx(ctx, forgetStep(keepLast(1)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if r.store.count("forget") != 1 {
		t.Errorf("restic forget ran %d times after the cancellation", r.store.count("forget"))
	}
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, r.store.forgotten())
	if out.ListedAt == nil || len(out.RemainingSnapshotIds) != 4 {
		t.Errorf("output %v", out)
	}
}

func TestИстёкшийСрокШагаПосредиУдаленияСообщаетУжеЗабытые(t *testing.T) {
	var snaps []restic.Snapshot
	for _, m := range daily("S", 3) {
		name := strings.TrimSpace(m.ID)
		snaps = append(snaps, m, part("f"+name, "S", name, m.Time))
	}
	r := newRig(t, noFloor, snaps)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	r.store.afterForget = func(int) { cancel() }
	out, err := r.runCtx(ctx, forgetStep(keepLast(1)))
	if err == nil {
		t.Fatal("no error")
	}
	sameSet(t, "forgotten", out.ForgottenSnapshotIds, r.store.forgotten())
}

func TestДействуетПределРепозиторияШагаАНеДругогоРепозиторияХоста(t *testing.T) {
	r := newRig(t, noFloor, weekly("S", 10))
	h := retention.New(retention.Options{
		Stores: func(string, string, io.Writer) (retention.Store, bool) { return r.store, true },
		Floors: func(name string) config.Floor {
			return map[string]config.Floor{"R": {KeepLast: 2, KeepWithinDays: 1}, "Q": {KeepLast: 50}}[name]
		},
		Now: func() time.Time { return now },
	})
	res, err := h.Run(context.Background(), forgetStep(keepLast(1)), r.log)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.GetForget().RemainingSnapshotIds); got != 2 {
		t.Errorf("%d snapshots remain, want the 2 of the floor of R", got)
	}
}

func TestОбработчикВыполняетТолькоForget(t *testing.T) {
	got := retention.New(retention.Options{}).Actions()
	if !slices.Equal(got, []agentv1.Action{agentv1.Action_ACTION_FORGET}) {
		t.Errorf("actions = %v", got)
	}
}

func TestПредельХостаОбслуживанияЗащищаетСнимкиСделанныеДругимХостом(t *testing.T) {
	snaps := weekly("S", 20)
	for i := range snaps {
		snaps[i].Hostname = "db1"
	}
	r := newRig(t, builtInFloor, snaps)
	out := r.mustRun(forgetStep(keepLast(1)))
	if len(out.RemainingSnapshotIds) != 7 || len(out.KeptByFloorSnapshotIds) != 6 {
		t.Errorf("remaining %d, kept by floor %d", len(out.RemainingSnapshotIds), len(out.KeptByFloorSnapshotIds))
	}
}
