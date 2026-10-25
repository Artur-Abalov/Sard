// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

var everyStage = []stage{
	{0, "snapshot", passed}, {0, "fingerprint_tag", passed}, {0, "work_volume", passed},
	{1, "restic_check", passed}, {2, "restore", passed}, {3, "start", notApplicable},
	{4, "fingerprint", passed}, {0, "cleanup", passed},
}

// --- a verification that passes

func TestAFaithfulCopyOfFilesPassesEveryStageAndReportsTheRestoreTime(t *testing.T) {
	r := newRig(t)

	result, err := r.run()

	v := result.GetVerify()
	if err != nil || v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_PASSED || v.GetSnapshotId() != snapshotID {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	if got := stagesOf(v); !slices.Equal(got, everyStage) {
		t.Fatalf("stages = %v", got)
	}
	if d := v.GetRestoreDuration().AsDuration(); d != 90*time.Second {
		t.Errorf("restore time = %v", d)
	}
	restore := find(v, "restore")
	if restore.GetStartedAt().AsTime() != t0 || restore.GetFinishedAt().AsTime() != t0.Add(90*time.Second) {
		t.Errorf("restore %v .. %v", restore.GetStartedAt().AsTime(), restore.GetFinishedAt().AsTime())
	}
}

func TestEachStageIsReportedAsItFinishes(t *testing.T) {
	r := newRig(t)

	result, _ := r.run()

	if len(r.reporter.stages) != len(everyStage) {
		t.Fatalf("reported %d stages", len(r.reporter.stages))
	}
	for i, s := range r.reporter.stages {
		if s.GetName() != result.GetVerify().GetStages()[i].GetName() {
			t.Errorf("stage %d reported as %q", i, s.GetName())
		}
	}
}

func TestTheCopyIsRestoredIntoTheDirectoryOfTheJobAndMeasuredAgainstTheSource(t *testing.T) {
	r := newRig(t)

	_, _ = r.run()

	dir := workDir + "/" + jobID
	if !slices.Equal(r.disk.made, []string{dir}) || !slices.Equal(r.store.restored, []string{snapshotID + " -> " + dir + "/copy"}) {
		t.Fatalf("made %q, restored %q", r.disk.made, r.store.restored)
	}
	requireMeasuredAgainstTheSource(t, r.plugin.measured, dir+"/copy")
	if !slices.Equal(r.store.percents, []int{10}) {
		t.Errorf("checked %v", r.store.percents)
	}
}

func requireMeasuredAgainstTheSource(t *testing.T, measured []sdk.FingerprintTarget, root string) {
	t.Helper()
	if len(measured) != 1 || measured[0].Root != root || measured[0].Reference == nil {
		t.Fatalf("measured %+v", measured)
	}
	if ref := *measured[0].Reference; ref.Plugin != "files" || len(ref.Facts) != 4 {
		t.Errorf("reference %+v", ref)
	}
}

// --- stage 0: the snapshot and the authenticity of the fingerprint (test 2)

func TestAFingerprintWhoseHashIsNotTheTagOfItsSnapshotFailsBeforeAnythingIsRead(t *testing.T) {
	r := newRig(t)
	forged := source()
	forged.Facts[2].Value = "300"
	r.step.Verify.Fingerprint, r.step.Verify.FingerprintSha256 = encoded(t, forged)

	result, err := r.run()

	v := result.GetVerify()
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_FAILED || err == nil || !strings.Contains(err.Error(), "fingerprint does not match the snapshot") {
		t.Fatalf("verdict %v, err %v", v.GetVerdict(), err)
	}
	want := []stage{{0, "snapshot", passed}, {0, "fingerprint_tag", failed}, {0, "cleanup", passed}}
	if got := stagesOf(v); !slices.Equal(got, want) {
		t.Fatalf("stages = %v", got)
	}
	if len(r.store.percents) != 0 || len(r.store.restored) != 0 || len(r.disk.made) != 0 {
		t.Errorf("read the repository: %v %v %v", r.store.percents, r.store.restored, r.disk.made)
	}
}

func TestAFingerprintThatIsNotWhatItsHashSaysFails(t *testing.T) {
	r := newRig(t)
	r.step.Verify.Fingerprint = append(r.step.Verify.Fingerprint, ' ')

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed || !strings.Contains(s.GetOutput(), "fingerprint does not match the snapshot") {
		t.Fatalf("stage = %v", s)
	}
}

func TestASnapshotWithoutTheTagOfAFingerprintFailsWhenTheTaskHasOne(t *testing.T) {
	r := newRig(t)
	r.store.snapshots = r.store.snapshots[:1]

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
		t.Fatalf("stage = %v", s)
	}
}

func TestAFingerprintSnapshotOfAnotherMainSnapshotDoesNotCount(t *testing.T) {
	r := newRig(t)
	other := strings.Repeat("e", 64)
	r.store.snapshots[1].Tags[1] = "files.main_snapshot=" + other

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
		t.Fatalf("stage = %v", s)
	}
}

func TestAHalfFingerprintInTheTaskFails(t *testing.T) {
	for _, half := range []func(*agentv1.VerifyTask){
		func(v *agentv1.VerifyTask) { v.Fingerprint = nil },
		func(v *agentv1.VerifyTask) { v.FingerprintSha256 = "" },
	} {
		r := newRig(t)
		half(r.step.Verify)

		result, _ := r.run()

		if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
			t.Errorf("stage = %v", s)
		}
	}
}

// A plugin that is no FingerprintCompleter (postgresql) tags the main snapshot.
func TestAFingerprintTaggedOnTheMainSnapshotIsAuthentic(t *testing.T) {
	r := newRig(t)
	_, hash := encoded(t, source())
	r.store.snapshots = []restic.Snapshot{{ID: snapshotID, Tags: []string{"sard.source=s1", "sard.fp=" + hash}}}

	result, err := verifier.New(r.opts).Run(t.Context(), r.step, r.plugin, r.store, r.reporter)

	if err != nil || result.GetVerify().GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_PASSED {
		t.Fatalf("result = %v, err = %v", result, err)
	}
	r.store.snapshots[0].Tags[1] = "sard.fp=" + strings.Repeat("0", 64)
	result, _ = verifier.New(r.opts).Run(t.Context(), r.step, r.plugin, r.store, r.reporter)
	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
		t.Fatalf("stage = %v", s)
	}
}

// A snapshot made before V2 has no fingerprint: it is verified up to the
// restore, and the data cannot be compared.
func TestASnapshotWithoutFingerprintIsRestoredButCannotBeVerified(t *testing.T) {
	r := newRig(t)
	r.step.Verify.Fingerprint, r.step.Verify.FingerprintSha256 = nil, ""
	r.store.snapshots = r.store.snapshots[:1]

	result, err := r.run()

	v := result.GetVerify()
	want := slices.Clone(everyStage)
	want[6].status = cannotRun
	if got := stagesOf(v); !slices.Equal(got, want) {
		t.Fatalf("stages = %v", got)
	}
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY || err == nil || !strings.Contains(err.Error(), "no fingerprint") {
		t.Fatalf("verdict %v, err %v", v.GetVerdict(), err)
	}
	if len(r.plugin.measured) != 0 || v.GetRestoreDuration().AsDuration() != 90*time.Second {
		t.Errorf("measured %v, restore time %v", r.plugin.measured, v.GetRestoreDuration())
	}
}

func TestASnapshotThatIsNotInTheRepositoryCannotBeVerified(t *testing.T) {
	r := newRig(t)
	r.store.snapshots = r.store.snapshots[1:]

	result, _ := r.run()

	s := find(result.GetVerify(), "snapshot")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "not in the repository") || result.GetVerify().GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY {
		t.Fatalf("stage = %v", s)
	}
}

func TestARepositoryThatCannotBeListedCannotBeVerified(t *testing.T) {
	r := newRig(t)
	r.store.listErr = fmt.Errorf("restic snapshots: %w", restic.ErrWrongPassword)

	result, _ := r.run()

	if s := find(result.GetVerify(), "snapshot"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "wrong password") {
		t.Fatalf("stage = %v", s)
	}
}

// --- stage 0: the work volume

func TestAWorkVolumeWithoutNoexecCannotVerify(t *testing.T) {
	r := newRig(t)
	r.disk.volume.NoExec = false

	result, _ := r.run()

	s := find(result.GetVerify(), "work_volume")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "not mounted noexec") || len(r.store.restored) != 0 {
		t.Fatalf("stage = %v", s)
	}
}

func TestTooLittleRoomForTheCopyCannotVerifyAndSaysHowMuchIsNeeded(t *testing.T) {
	r := newRig(t)
	r.disk.volume.Free = 2*2048 + 1<<30 - 1

	result, _ := r.run()

	s := find(result.GetVerify(), "work_volume")
	if s.GetStatus() != cannotRun || s.GetOutput() != "the copy needs 1073745920 bytes free on "+workDir+", 1073745919 are" {
		t.Fatalf("stage = %v", s)
	}
	r = newRig(t)
	r.disk.volume.Free = 2*2048 + 1<<30
	if result, _ = r.run(); find(result.GetVerify(), "work_volume").GetStatus() != passed {
		t.Fatalf("exactly enough room: %v", find(result.GetVerify(), "work_volume"))
	}
}

func TestACopyOfUnknownSizeGoesOnAndSaysSo(t *testing.T) {
	r := newRig(t)
	r.plugin.sized = false

	result, _ := r.run()

	if s := find(result.GetVerify(), "work_volume"); s.GetStatus() != passed || !strings.Contains(s.GetOutput(), "size of the copy is unknown") {
		t.Fatalf("stage = %v", s)
	}
}

func TestAWorkVolumeThatCannotBeMeasuredCannotVerify(t *testing.T) {
	r := newRig(t)
	r.disk.volumeErr = errors.New("statfs: no such file or directory")

	result, _ := r.run()

	if s := find(result.GetVerify(), "work_volume"); s.GetStatus() != cannotRun {
		t.Fatalf("stage = %v", s)
	}
}

// --- level 1: restic check (test 3)

func TestADamagedRepositoryFailsAtLevelOneWithWhatResticFound(t *testing.T) {
	r := newRig(t)
	r.store.checks = []checkReply{{report: restic.CheckReport{Errors: []string{"pack 7054: does not exist"}}, err: fmt.Errorf("%w: %w", restic.ErrDamaged, &restic.ExitError{Code: 1, Message: "Fatal: repository contains errors"})}}

	result, err := r.run()

	v := result.GetVerify()
	s := find(v, "restic_check")
	if s.GetStatus() != failed || s.GetExitCode() != 1 || !strings.Contains(s.GetOutput(), "pack 7054: does not exist") {
		t.Fatalf("stage = %v", s)
	}
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_FAILED || err == nil || len(r.store.restored) != 0 {
		t.Fatalf("verdict %v, err %v, restored %v", v.GetVerdict(), err, r.store.restored)
	}
}

func TestARepositoryCheckCouldNotOpenCannotBeVerified(t *testing.T) {
	r := newRig(t)
	r.store.checks = []checkReply{{err: &restic.ExitError{Code: 1, Message: "Fatal: unable to open repository"}}}

	result, _ := r.run()

	if s := find(result.GetVerify(), "restic_check"); s.GetStatus() != cannotRun || s.GetExitCode() != 1 {
		t.Fatalf("stage = %v", s)
	}
}

// A prune of the source while the check reads without a lock removes packs
// the check expected: that says nothing about the backup.
func TestErrorsWhileThePruneOfTheSourceChangedTheRepositoryAreCheckedOnceMore(t *testing.T) {
	r := newRig(t)
	pruned := append(slices.Clone(r.store.snapshots), restic.Snapshot{ID: strings.Repeat("a", 64)})
	damaged := fmt.Errorf("%w", restic.ErrDamaged)
	r.store.checks = []checkReply{{err: damaged, snapshots: pruned}, {}}

	result, err := r.run()

	if err != nil || find(result.GetVerify(), "restic_check").GetStatus() != passed || len(r.store.percents) != 2 {
		t.Fatalf("checks %v, stage %v, err %v", r.store.percents, find(result.GetVerify(), "restic_check"), err)
	}
}

func TestARepositoryThatKeepsChangingDuringTheCheckCannotBeVerified(t *testing.T) {
	r := newRig(t)
	first := append(slices.Clone(r.store.snapshots), restic.Snapshot{ID: strings.Repeat("a", 64)})
	damaged := fmt.Errorf("%w", restic.ErrDamaged)
	r.store.checks = []checkReply{{err: damaged, snapshots: first}, {err: damaged, snapshots: first[:2]}}

	result, _ := r.run()

	s := find(result.GetVerify(), "restic_check")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "the repository changed during the check") || len(r.store.percents) != 2 {
		t.Fatalf("stage = %v", s)
	}
}

func TestErrorsInAnUnchangedRepositoryAreNotCheckedAgain(t *testing.T) {
	r := newRig(t)
	r.store.checks = []checkReply{{err: fmt.Errorf("%w", restic.ErrDamaged)}, {}}

	result, _ := r.run()

	if find(result.GetVerify(), "restic_check").GetStatus() != failed || len(r.store.percents) != 1 {
		t.Fatalf("checks %v", r.store.percents)
	}
}

// --- level 2: the restore

func TestARestoreThatReadsBackDifferentDataFails(t *testing.T) {
	r := newRig(t)
	r.store.restoreErr = &restic.ExitError{Code: 1, Message: "Fatal: There were 1 errors"}

	result, _ := r.run()

	if s := find(result.GetVerify(), "restore"); s.GetStatus() != failed || s.GetExitCode() != 1 {
		t.Fatalf("stage = %v", s)
	}
	if result.GetVerify().GetRestoreDuration() != nil {
		t.Errorf("a failed restore has a restore time")
	}
}

func TestARestoreTheEnvironmentStoppedCannotVerify(t *testing.T) {
	for _, cause := range []error{restic.ErrNetwork, restic.ErrNoSpace, restic.ErrWrongPassword, restic.ErrNoRepository, restic.ErrLocked} {
		r := newRig(t)
		r.store.restoreErr = fmt.Errorf("restic restore: %w", cause)

		result, _ := r.run()

		if s := find(result.GetVerify(), "restore"); s.GetStatus() != cannotRun {
			t.Errorf("%v: stage = %v", cause, s)
		}
	}
}

// --- level 4: the data (test 4 for files)

func TestACopyThatDiffersFromTheSourceFailsAtLevelFourListingTheFacts(t *testing.T) {
	r := newRig(t)
	r.plugin.copied.Facts[2].Value = "2"

	result, err := r.run()

	v := result.GetVerify()
	s := find(v, "fingerprint")
	if s.GetStatus() != failed || !strings.Contains(s.GetOutput(), "files: 3 in the source, 2 in the copy") {
		t.Fatalf("stage = %v", s)
	}
	want := []*agentv1.FingerprintMismatch{{Name: "files", Expected: "3", Actual: "2"}}
	if len(v.GetMismatches()) != 1 || v.GetMismatches()[0].String() != want[0].String() {
		t.Fatalf("mismatches = %v", v.GetMismatches())
	}
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_FAILED || err == nil {
		t.Fatalf("verdict %v, err %v", v.GetVerdict(), err)
	}
}

func TestAFactOfTheCopyMeasuredOnlyForInformationCannotVerify(t *testing.T) {
	r := newRig(t)
	r.plugin.copied.Facts[2].Rule = sdk.RuleInfo

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "files") {
		t.Fatalf("stage = %v", s)
	}
}

func TestACopyThatCannotBeMeasuredCannotVerify(t *testing.T) {
	r := newRig(t)
	r.plugin.measure = errors.New("open /srv/data: permission denied")

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "permission denied") {
		t.Fatalf("stage = %v", s)
	}
}

// --- the limits of the stages (test 5): the verification did not take place

func TestAStageThatRunsOutOfItsTimeIsAnInfrastructureFailureNamingIt(t *testing.T) {
	r := newRig(t)
	r.store.restoreTook = 4 * time.Hour

	result, err := r.run()

	v := result.GetVerify()
	if v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_INFRASTRUCTURE_FAILURE || err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("verdict %v, err %v", v.GetVerdict(), err)
	}
	s := find(v, "restore")
	if s.GetStatus() != cannotRun || s.GetOutput() != "the stage ran out of its time limit of 4h0m0s" {
		t.Fatalf("stage = %v", s)
	}
	if last := v.GetStages()[len(v.GetStages())-1]; last.GetName() != "cleanup" || last.GetStatus() != passed {
		t.Fatalf("last stage = %v", last)
	}
}

func TestAStageGetsNoMoreTimeThanTheJobHasLeft(t *testing.T) {
	r := newRig(t)
	r.step.Verify.Deadline.Seconds = t0.Add(time.Hour).Unix()
	r.store.restoreTook = time.Hour

	result, _ := r.run()

	if s := find(result.GetVerify(), "restore"); s.GetOutput() != "the stage ran out of its time limit of 1h0m0s" {
		t.Fatalf("stage = %v", s)
	}
}

// --- what is refused before anything is touched (test 8)

func TestAVerifierRefusesASourceOfItsOwnMachine(t *testing.T) {
	r := newRig(t)
	r.step.Verify.SourceMachineIds = append(r.step.Verify.SourceMachineIds, ownMachine)

	result, err := r.run()

	if !errors.Is(err, executor.ErrRejected) || result != nil || !strings.Contains(err.Error(), "own machine") {
		t.Fatalf("result %v, err %v", result, err)
	}
	if len(r.reporter.stages) != 0 || len(r.disk.made) != 0 || len(r.store.percents) != 0 {
		t.Errorf("something was touched")
	}
}

func TestAStepThatIsNoVerificationOfAWholeSnapshotIsRejected(t *testing.T) {
	for name, spoil := range map[string]func(*agentv1.RunStep){
		"no task":       func(s *agentv1.RunStep) { s.Verify = nil },
		"no job":        func(s *agentv1.RunStep) { s.Verify.JobId = "" },
		"job as a path": func(s *agentv1.RunStep) { s.Verify.JobId = "../etc" },
		"short id":      func(s *agentv1.RunStep) { s.SnapshotId = "6c719cbc" },
		"no deadline":   func(s *agentv1.RunStep) { s.Verify.Deadline = nil },
	} {
		r := newRig(t)
		spoil(r.step)

		if _, err := r.run(); !errors.Is(err, executor.ErrRejected) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestOneVerificationRunsAtATime(t *testing.T) {
	r := newRig(t)
	v := verifier.New(r.opts)
	var wg sync.WaitGroup
	inside := make(chan struct{})
	release := make(chan struct{})
	r.plugin.measure = nil
	blocking := &blockingStore{fakeStore: r.store, inside: inside, release: release}
	wg.Go(func() { _, _ = v.Run(t.Context(), r.step, completing{r.plugin}, blocking, r.reporter) })
	<-inside

	_, err := v.Run(t.Context(), r.step, completing{r.plugin}, r.store, &fakeReporter{})

	close(release)
	wg.Wait()
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v", err)
	}
	if _, err := v.Run(t.Context(), r.step, completing{r.plugin}, r.store, &fakeReporter{}); err != nil {
		t.Fatalf("after the first: %v", err)
	}
}

// --- what stays on the disk (test 7)

func TestTheDirectoryOfTheJobIsRemovedWhateverTheOutcome(t *testing.T) {
	for name, spoil := range map[string]func(*rig){
		"passed":        func(*rig) {},
		"failed":        func(r *rig) { r.plugin.copied.Facts[2].Value = "2" },
		"cannot verify": func(r *rig) { r.plugin.measure = errors.New("eio") },
		"out of time":   func(r *rig) { r.store.restoreTook = 5 * time.Hour },
	} {
		r := newRig(t)
		spoil(r)

		_, _ = r.run()

		if !slices.Equal(r.disk.removed, []string{workDir + "/" + jobID}) {
			t.Errorf("%s: removed %q", name, r.disk.removed)
		}
	}
}

func TestAJobDirectoryThatCannotBeRemovedMakesAPassedVerificationUnproven(t *testing.T) {
	r := newRig(t)
	r.disk.removeErr = errors.New("unlinkat: device or resource busy")

	result, err := r.run()

	v := result.GetVerify()
	s := find(v, "cleanup")
	if s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), workDir+"/"+jobID) || v.GetVerdict() != agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY || err == nil {
		t.Fatalf("stage %v, verdict %v, err %v", s, v.GetVerdict(), err)
	}
}

func TestAFailedVerificationStaysFailedWhenItsDirectoryCannotBeRemoved(t *testing.T) {
	r := newRig(t)
	r.plugin.copied.Facts[2].Value = "2"
	r.disk.removeErr = errors.New("busy")

	result, _ := r.run()

	if v := result.GetVerify().GetVerdict(); v != agentv1.VerifyVerdict_VERIFY_VERDICT_FAILED {
		t.Fatalf("verdict %v", v)
	}
}

func TestAtStartEverythingLeftByEarlierVerificationsIsRemoved(t *testing.T) {
	r := newRig(t)
	r.disk.entries = []string{jobID, "7a7a7a7a-0000-0000-0000-000000000000"}

	removed, err := verifier.New(r.opts).Sweep()

	want := []string{workDir + "/" + jobID, workDir + "/7a7a7a7a-0000-0000-0000-000000000000"}
	if err != nil || !slices.Equal(removed, want) || !slices.Equal(r.disk.removed, want) {
		t.Fatalf("removed %q (%q), err %v", removed, r.disk.removed, err)
	}
}

// --- which plugins a verifier can verify

func TestOnlyAPluginThatMeasuresAndRestoresACopyCanBeVerified(t *testing.T) {
	r := newRig(t)
	if !verifier.Supports(r.plugin) || verifier.Supports(struct{ sdk.Fingerprinter }{r.plugin}) {
		t.Fatal("Supports")
	}
}

// blockingStore holds the first check until released.
type blockingStore struct {
	*fakeStore
	inside, release chan struct{}
}

func (b *blockingStore) VerifyData(ctx context.Context, percent int) (restic.CheckReport, error) {
	close(b.inside)
	<-b.release
	return b.fakeStore.VerifyData(ctx, percent)
}

// A fingerprint the tag vouches for but that is no canonical fingerprint was
// spoiled before it was tagged: the backup's evidence is bad.
func TestAnAuthenticFingerprintThatCannotBeReadFails(t *testing.T) {
	r := newRig(t)
	odd := []byte(`{"plugin":"files"}`)
	hash := sdk.HashBytes(odd)
	r.step.Verify.Fingerprint, r.step.Verify.FingerprintSha256 = odd, hash
	r.store.snapshots[1].Tags[3] = "sard.fp=" + hash

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed || !strings.Contains(s.GetOutput(), "invalid fingerprint") {
		t.Fatalf("stage = %v", s)
	}
}

func TestDamageWhoseSnapshotsCannotBeListedAgainCannotBeVerified(t *testing.T) {
	r := newRig(t)
	r.store.checks = []checkReply{{err: fmt.Errorf("%w", restic.ErrDamaged)}}
	calls := 0
	lister := &listOnce{fakeStore: r.store, calls: &calls}

	result, _ := newVerifier(r).Run(t.Context(), r.step, completing{r.plugin}, lister, r.reporter)

	if s := find(result.GetVerify(), "restic_check"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "cannot list the snapshots") {
		t.Fatalf("stage = %v", s)
	}
}
