// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/fingerprint"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// fingerprintTag is the tag a backup gives the snapshot that holds its
// fingerprint: sard.fp=<hex SHA-256 of the canonical form> (V2).
const fingerprintTag = "sard.fp="

// forged is how a fingerprint the snapshot does not vouch for is reported.
const forged = "fingerprint does not match the snapshot (changed on the server, or damaged)"

// snapshot: the snapshot is in the repository, read with the verifier's key.
func (x *run) snapshot(ctx context.Context) outcome {
	listed, err := x.store.Snapshots(ctx, nil)
	if err != nil {
		return withExit(cannotRun("cannot list the snapshots: %v", err), err)
	}
	for _, s := range listed {
		x.ids = append(x.ids, s.ID)
	}
	x.listed = listed
	if !slices.Contains(x.ids, x.step.GetSnapshotId()) {
		return cannotRun("snapshot %s is not in the repository (forgotten since?)", x.step.GetSnapshotId())
	}
	return passed("snapshot %s is in the repository", x.step.GetSnapshotId())
}

// fingerprintTag: the fingerprint of the job is the one the backup tagged.
// A job without one (a snapshot made before V2) goes on without a reference.
func (x *run) fingerprintTag(context.Context) outcome {
	task := x.step.GetVerify()
	if len(task.GetFingerprint()) == 0 && task.GetFingerprintSha256() == "" {
		return passed("the snapshot has no fingerprint: it was made before fingerprints, its data cannot be compared")
	}
	hash := task.GetFingerprintSha256()
	if sdk.HashBytes(task.GetFingerprint()) != hash || !x.tagged(hash) {
		return failed(forged)
	}
	ref, err := sdk.DecodeFingerprint(task.GetFingerprint())
	if err != nil {
		return failed("%s: %v", forged, err)
	}
	x.reference = &ref
	return passed("the fingerprint is the one tagged %s%s", fingerprintTag, hash)
}

// tagged: the snapshot that holds the fingerprint carries the hash. A plugin
// that completes its fingerprint after the backup stores it as a snapshot of
// its own, linked to the main one; any other tags the main snapshot.
func (x *run) tagged(hash string) bool {
	tag, main := fingerprintTag+hash, x.step.GetSnapshotId()
	if _, completes := x.plugin.(sdk.FingerprintCompleter); !completes {
		return slices.ContainsFunc(x.listed, func(s restic.Snapshot) bool { return s.ID == main && slices.Contains(s.Tags, tag) })
	}
	name := x.plugin.Name()
	want := []string{tag, name + ".main_snapshot=" + main, name + ".part=fingerprint"}
	return slices.ContainsFunc(x.listed, func(s restic.Snapshot) bool { return containsAll(s.Tags, want) })
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// check: restic check reads a share of the data (level 1). Errors found while
// the repository changed under the check (a prune of the source: the check
// takes no lock) are checked once more; changing again, they say nothing
// about the backup.
func (x *run) check(ctx context.Context) outcome {
	for attempt := 1; ; attempt++ {
		report, err := x.store.VerifyData(ctx, x.opts.CheckPercent)
		if !errors.Is(err, restic.ErrDamaged) {
			return x.checked(report, err)
		}
		changed, listing := x.relisted(ctx)
		switch {
		case !listing.proves():
			return listing
		case !changed:
			return withExit(failed("%s", found(report, err)), err)
		case attempt == 2:
			return cannotRun("the repository changed during the check, twice (a prune of the source?): %s", found(report, err))
		}
	}
}

// checked is the outcome of a check that found no damage.
func (x *run) checked(report restic.CheckReport, err error) outcome {
	if err != nil {
		return withExit(cannotRun("restic check did not run to the end: %s", found(report, err)), err)
	}
	return passed("restic check read %d%% of the data and found no errors", x.opts.CheckPercent)
}

// relisted lists the snapshots again and says whether they changed, with
// the outcome of the listing.
func (x *run) relisted(ctx context.Context) (bool, outcome) {
	before := x.ids
	x.ids, x.listed = nil, nil
	listing := x.snapshot(ctx)
	return !slices.Equal(before, x.ids), listing
}

// found is what restic said, one finding per line, then its error.
func found(report restic.CheckReport, err error) string {
	return strings.Join(append(slices.Clone(report.Errors), err.Error()), "\n")
}

// withExit sets the exit code of restic, if err carries one.
func withExit(o outcome, err error) outcome {
	var exit *restic.ExitError
	if errors.As(err, &exit) {
		o.exitCode = int32(exit.Code)
	}
	return o
}

// restoreCopy: the plugin restores the snapshot through the host (level 2).
func (x *run) restoreCopy(ctx context.Context) outcome {
	req := sdk.RestoreRequest{
		CommandID: x.step.GetCommandId(), SnapshotID: x.step.GetSnapshotId(),
		Repository: x.step.GetRepositoryName(), Source: x.step.GetTags()["sard.source"],
	}
	copied, err := x.plugin.RestoreInto(ctx, x.host(), sdk.Config("{}"), req)
	if err != nil {
		return withExit(restoreFailure(err), err)
	}
	x.copied = copied
	return passed("restored into %s", copied.Result.Target)
}

// environment are the failures of a restore that say nothing about the backup.
var environment = []error{
	restic.ErrNetwork, restic.ErrNoSpace, restic.ErrWrongPassword, restic.ErrNoRepository, restic.ErrLocked,
	context.Canceled, context.DeadlineExceeded,
}

func restoreFailure(err error) outcome {
	if slices.ContainsFunc(environment, func(e error) bool { return errors.Is(err, e) }) {
		return cannotRun("the restore could not run here: %v", err)
	}
	return failed("the snapshot could not be restored: %v", err)
}

// start: the application the copy needs (level 3). A tree of files needs none.
func (x *run) start(context.Context) outcome {
	return outcome{status: agentv1.StageStatus_STAGE_STATUS_NOT_APPLICABLE, output: "a copy of " + x.plugin.Name() + " needs no application to start"}
}

// fingerprint: the copy, measured by the plugin, matches the source (level 4).
func (x *run) fingerprint(ctx context.Context) outcome {
	if x.reference == nil {
		return cannotRun("no fingerprint: the snapshot was made before fingerprints, the copy cannot be compared with the source")
	}
	target := sdk.FingerprintTarget{Root: x.copied.Root, Reference: x.reference}
	got, err := x.plugin.Fingerprint(ctx, x.host(), x.copied.Config, target)
	if err != nil {
		return cannotRun("the copy could not be measured: %v", err)
	}
	return x.compared(fingerprint.Compare(*x.reference, got))
}

func (x *run) compared(res fingerprint.Result) outcome {
	if len(res.Mismatches) > 0 {
		lines := make([]string, len(res.Mismatches))
		for i, m := range res.Mismatches {
			lines[i] = m.Text()
			x.out.Mismatches = append(x.out.Mismatches, &agentv1.FingerprintMismatch{Name: m.Name, Expected: m.Expected, Actual: m.Actual})
		}
		return failed("%s", strings.Join(lines, "\n"))
	}
	if len(res.Unverified) > 0 {
		return cannotRun("the copy could only measure for information: %s", strings.Join(res.Unverified, ", "))
	}
	return passed("the copy matches the fingerprint of the source: %d facts", len(x.reference.Facts))
}

// cleanup removes the directory of the job, whatever happened before.
func (x *run) cleanup(context.Context) outcome {
	if err := x.opts.Disk.RemoveAll(x.dir); err != nil {
		return cannotRun("cannot remove %s, remove it by hand: %v", x.dir, err)
	}
	return passed("removed %s", x.dir)
}
