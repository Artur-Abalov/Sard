// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The cases of this file were found by mutation testing: each pins a
// boundary or a branch the scenarios pass over.

func TestAPluginLineOfAnUnknownLevelIsInfo(t *testing.T) {
	r, p := streamingRig(t)
	p.level = sdk.Level(99)

	_, _ = newVerifier(r).Run(t.Context(), r.step, p, r.store, r.reporter)

	if len(r.reporter.levels) != 2 || r.reporter.levels[1] != agentv1.LogLevel_LOG_LEVEL_INFO {
		t.Fatalf("levels %v", r.reporter.levels)
	}
}

func TestAnOutputOfTheLimitIsKeptWholeAndALongerOneByItsTail(t *testing.T) {
	for _, n := range []int{4096, 4097} {
		r := newRig(t)
		long := strings.Repeat("x", n-1) + "E"
		r.store.checks = []checkReply{{report: restic.CheckReport{Errors: []string{long}}, err: errors.New("x")}}

		result, _ := r.run()

		out := find(result.GetVerify(), "restic_check").GetOutput()
		want := "restic check did not run to the end: " + long + "\nx"
		if len(want) > 4096 {
			want = "…" + want[len(want)-4096:]
		}
		if out != want {
			t.Errorf("%d: output of %d bytes, want %d", n, len(out), len(want))
		}
	}
}

func TestTheShortOutputLimitBoundary(t *testing.T) {
	r := newRig(t)
	exact := strings.Repeat("y", 4096-len("restic check did not run to the end: ")-2) + "\nz"
	r.store.checks = []checkReply{{report: restic.CheckReport{Errors: []string{exact[:len(exact)-2]}}, err: errors.New("z")}}

	result, _ := r.run()

	if out := find(result.GetVerify(), "restic_check").GetOutput(); len(out) != 4096 || strings.HasPrefix(out, "…") {
		t.Fatalf("output of %d bytes: %.20q", len(out), out)
	}
}

func TestMeasuringTheCopyIsNoPartOfTheRestoreTime(t *testing.T) {
	r := newRig(t)
	r.plugin.measureTook = time.Minute

	result, _ := r.run()

	if d := result.GetVerify().GetRestoreDuration().AsDuration(); d != 90*time.Second {
		t.Fatalf("restore time %v", d)
	}
}

func TestAJobPastItsDeadlineGivesAStageNoTime(t *testing.T) {
	r := newRig(t)
	r.step.Verify.Deadline.Seconds = t0.Add(-time.Hour).Unix()

	result, _ := r.run()

	if s := find(result.GetVerify(), "restore"); s.GetOutput() != "the stage ran out of its time limit of 0s" {
		t.Fatalf("stage = %v", s)
	}
}

func TestEveryStageStopsItsTimerAndEndsItsContext(t *testing.T) {
	r := newRig(t)

	_, _ = r.run()

	for i, timer := range r.clock.timers {
		if !timer.stopped {
			t.Errorf("timer %d of %d still runs", i, len(r.clock.timers))
		}
	}
	for i, ctx := range r.plugin.ctxs {
		if ctx.Err() == nil {
			t.Errorf("context %d of the plugin is still live", i)
		}
	}
}

func TestAFingerprintOfOtherBytesUnderTheTaggedHashFails(t *testing.T) {
	r := newRig(t)
	other := source()
	other.Facts[0].Value = "4096"
	r.step.Verify.Fingerprint, _ = encoded(t, other) // the hash stays the tagged one

	result, _ := r.run()

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
		t.Fatalf("stage = %v", s)
	}
}

func TestATagOnAnotherSnapshotDoesNotVouchForTheMainOne(t *testing.T) {
	r := newRig(t)
	_, hash := encoded(t, source())
	r.store.snapshots = []restic.Snapshot{{ID: snapshotID}, {ID: strings.Repeat("e", 64), Tags: []string{"sard.fp=" + hash}}}

	result, _ := newVerifier(r).Run(t.Context(), r.step, r.plugin, r.store, r.reporter)

	if s := find(result.GetVerify(), "fingerprint_tag"); s.GetStatus() != failed {
		t.Fatalf("stage = %v", s)
	}
}

func TestOnlyFullLowerCaseHexIsASnapshotID(t *testing.T) {
	valid := strings.Repeat("0123456789abcdef", 4)
	for _, id := range []string{valid, strings.Repeat("9", 64), strings.Repeat("a", 64), strings.Repeat("f", 64)} {
		r := newRig(t)
		r.step.SnapshotId = id
		r.store.snapshots[0].ID = id
		if _, err := r.run(); err != nil && strings.Contains(err.Error(), "snapshot_id") {
			t.Errorf("%s refused: %v", id, err)
		}
	}
	for _, c := range []string{"/", ":", "`", "g", "A", "G"} {
		r := newRig(t)
		r.step.SnapshotId = c + valid[1:]
		if _, err := r.run(); err == nil || !strings.Contains(err.Error(), "snapshot_id") {
			t.Errorf("%q accepted: %v", c, err)
		}
	}
}

func TestASweepThatCannotListOrRemoveSaysSo(t *testing.T) {
	r := newRig(t)
	lister := &brokenEntries{fakeDisk: r.disk}
	r.opts.Disk = lister
	if removed, err := verifier.New(r.opts).Sweep(); err == nil || removed != nil {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	r = newRig(t)
	r.disk.entries = []string{"a", "b"}
	stuck := &stuckSecond{fakeDisk: r.disk}
	r.opts.Disk = stuck
	removed, err := verifier.New(r.opts).Sweep()
	if err == nil || len(removed) != 1 || removed[0] != workDir+"/a" {
		t.Fatalf("removed %v, err %v", removed, err)
	}
}

type brokenEntries struct{ *fakeDisk }

func (*brokenEntries) Entries(string) ([]string, error) { return nil, errors.New("eio") }

type stuckSecond struct{ *fakeDisk }

func (s *stuckSecond) RemoveAll(p string) error {
	if strings.HasSuffix(p, "/b") {
		return errors.New("busy")
	}
	return s.fakeDisk.RemoveAll(p)
}

func TestTooLittleRoomMakesNoDirectory(t *testing.T) {
	r := newRig(t)
	r.disk.volume.Free = 1

	_, _ = r.run()

	if len(r.disk.made) != 0 {
		t.Fatalf("made %v", r.disk.made)
	}
}

func TestADirectoryOfTheJobThatCannotBeMadeCannotVerify(t *testing.T) {
	r := newRig(t)
	r.disk.makeErr = errors.New("file exists")

	result, _ := r.run()

	if s := find(result.GetVerify(), "work_volume"); s.GetStatus() != cannotRun || !strings.Contains(s.GetOutput(), "file exists") {
		t.Fatalf("stage = %v", s)
	}
}

func TestAPluginThatCannotSizeACopyGoesOnWithoutReservingRoom(t *testing.T) {
	r, _ := streamingRig(t)
	p := struct{ verifier.Plugin }{r.plugin} // no CopySizer

	result, _ := newVerifier(r).Run(t.Context(), r.step, p, r.store, r.reporter)

	if s := find(result.GetVerify(), "work_volume"); !strings.Contains(s.GetOutput(), "size of the copy is unknown") {
		t.Fatalf("stage = %v", s)
	}
}

func TestASnapshotWithoutFingerprintReservesNoRoom(t *testing.T) {
	r := newRig(t)
	r.step.Verify.Fingerprint, r.step.Verify.FingerprintSha256 = nil, ""

	result, _ := r.run()

	if s := find(result.GetVerify(), "work_volume"); !strings.Contains(s.GetOutput(), "size of the copy is unknown") {
		t.Fatalf("stage = %v", s)
	}
}

func TestTheRoomOfAHugeCopyIsTheLargestNumber(t *testing.T) {
	for _, space := range []verifier.Space{{Factor: 1}, {Factor: 1, Headroom: 1 << 63}} {
		r := newRig(t)
		r.opts.Space = space
		r.plugin.size = math.MaxUint64
		r.disk.volume.Free = math.MaxUint64

		result, _ := r.run()

		if s := find(result.GetVerify(), "work_volume"); !strings.Contains(s.GetOutput(), "needs 18446744073709551615 bytes") {
			t.Errorf("%+v: stage = %v", space, s)
		}
	}
}

func TestAHeartbeatBeforeAnyRegisterCarriesTheStatus(t *testing.T) {
	s := newStatus(&fakeDisk{volume: verifier.Volume{Free: 1}}, &runtimeCLI{}, &fakeClock{now: t0})

	if s.Changed(t.Context()) == nil {
		t.Fatal("nothing reported yet, and nothing sent")
	}
}

func TestTheRuntimeIsAskedAtTheFirstMeasureWhateverTheClock(t *testing.T) {
	s := newStatus(&fakeDisk{}, &runtimeCLI{version: "29.9.2"}, &fakeClock{})

	if got := s.Current(t.Context()); got.GetSandbox() != agentv1.Sandbox_SANDBOX_DOCKER {
		t.Fatalf("status = %v", got)
	}
}

// One percent of the free space is the change the server hears of.
func TestAChangeOfOnePercentIsReportedAndLessIsNot(t *testing.T) {
	for _, c := range []struct {
		free    uint64
		changed bool
	}{{10_000 - 100, false}, {10_000 - 101, true}, {10_000 + 100, false}, {10_000 + 101, true}} {
		disk := &fakeDisk{volume: verifier.Volume{Free: 10_000}}
		s := newStatus(disk, &runtimeCLI{}, &fakeClock{now: t0})
		_ = s.Current(t.Context())
		disk.volume.Free = c.free
		if got := s.Changed(t.Context()) != nil; got != c.changed {
			t.Errorf("free %d: changed %v", c.free, got)
		}
	}
}

func TestAWorkVolumeThatCannotBeMeasuredAnyMoreIsAChange(t *testing.T) {
	disk := &fakeDisk{volume: verifier.Volume{Free: 0}}
	s := newStatus(disk, &runtimeCLI{}, &fakeClock{now: t0})
	_ = s.Current(t.Context())
	disk.volumeErr = errors.New("gone")

	if got := s.Changed(t.Context()); got == nil || got.WorkFreeBytes != nil {
		t.Fatalf("status = %v", got)
	}
}
