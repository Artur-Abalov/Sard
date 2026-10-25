// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package verifier runs a restore check on a verifier (V5): it proves that the
// fingerprint of the job is the one the backup tagged, checks the repository,
// restores the snapshot into a directory of its own, measures the copy with
// the plugin's own code and compares it with the source. Every stage is
// reported as it ends. It reads the repository only, never through a lock, and
// removes what it restored whatever the outcome. It knows no plugin by name.
package verifier

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Store is the repository of a step as a verifier reads it: without a lock,
// with a key and storage that may only read (restic.CLI).
type Store interface {
	Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error)
	VerifyData(ctx context.Context, percent int) (restic.CheckReport, error)
	RestoreVerified(ctx context.Context, snapshotID, target string) error
	Dump(ctx context.Context, snapshotID, path string, w io.Writer) error
}

// Volume is what the work volume offers a copy.
type Volume struct {
	// Free is the number of bytes the service user may still write.
	Free uint64
	// NoExec: nothing on the volume can be executed.
	NoExec bool
}

// Disk is the work volume of the verifier.
type Disk interface {
	Volume(dir string) (Volume, error)
	// MakeDir makes a new directory, 0700; an existing one is an error.
	MakeDir(path string) error
	RemoveAll(path string) error
	// Entries lists the names in dir; a missing dir has none.
	Entries(dir string) ([]string, error)
}

// Limits are the longest each kind of stage may take; the time the job has
// left shortens them (VerifyTask.deadline).
type Limits struct {
	Prepare, Check, Restore, Fingerprint time.Duration
}

// Space is the room a copy needs: its size times Factor plus Headroom bytes.
type Space struct {
	Factor   float64
	Headroom uint64
}

// Options configure a Verifier.
type Options struct {
	// WorkDir holds one directory per job, named by the job id.
	WorkDir string
	// MachineID is the machine id of this host (package machineid).
	MachineID string
	// CheckPercent is the share of the data restic check reads.
	CheckPercent int
	Limits       Limits
	Space        Space
	Clock        executor.Clock
	Disk         Disk
}

// Plugin is a plugin whose snapshots can be verified.
type Plugin interface {
	Name() string
	sdk.Fingerprinter
	sdk.CopyRestorer
}

// Supports reports whether p can be verified: it measures a copy and restores one.
func Supports(p any) bool {
	_, ok := p.(Plugin)
	return ok
}

// Verifier runs one verification at a time.
type Verifier struct {
	opts Options
	busy atomic.Bool
}

// New returns a Verifier.
func New(opts Options) *Verifier { return &Verifier{opts: opts} }

// Run verifies the snapshot of the step. A step it refuses, before anything is
// read or written, wraps executor.ErrRejected. Otherwise the result carries
// the stages and the verdict; a verdict other than passed comes with an error
// that says why, so that the step fails with it.
func (v *Verifier) Run(ctx context.Context, step *agentv1.RunStep, p Plugin, store Store, r executor.Reporter) (*agentv1.StepResult, error) {
	if err := v.admit(step); err != nil {
		return nil, fmt.Errorf("%w: %w", executor.ErrRejected, err)
	}
	if !v.busy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%w: a verification is already running on this verifier", executor.ErrRejected)
	}
	defer v.busy.Store(false)
	return newRun(ctx, v, step, p, store, r).execute()
}

// admit judges the step itself: a verification of a whole snapshot, for a job
// that can name a directory, of a source on another machine (V-D7).
func (v *Verifier) admit(step *agentv1.RunStep) error {
	if err := wellFormed(step); err != nil {
		return err
	}
	if slices.Contains(step.GetVerify().GetSourceMachineIds(), v.opts.MachineID) {
		return errors.New("the source is on this verifier's own machine: a verifier never checks a source on its own host")
	}
	return nil
}

func wellFormed(step *agentv1.RunStep) error {
	task := step.GetVerify()
	switch {
	case task == nil || task.GetDeadline() == nil:
		return errors.New("a verify step needs its task and the deadline of the job")
	case task.GetJobId() == "" || !consolerestore.UsableName(task.GetJobId()):
		return fmt.Errorf("job id %q cannot name a directory", task.GetJobId())
	case !snapshotID(step.GetSnapshotId()):
		return fmt.Errorf("snapshot_id %q: want the full id of the snapshot", step.GetSnapshotId())
	}
	return nil
}

// snapshotID: 64 lower-case hexadecimal characters.
func snapshotID(id string) bool {
	return len(id) == 64 && !strings.ContainsFunc(id, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}

// Sweep removes what verifications that did not end left in the work
// directory (the agent stopped or was killed); it runs at start, before any
// step, and returns what it removed.
func (v *Verifier) Sweep() ([]string, error) {
	names, err := v.opts.Disk.Entries(v.opts.WorkDir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		dir := path.Join(v.opts.WorkDir, name)
		if err := v.opts.Disk.RemoveAll(dir); err != nil {
			return removed, fmt.Errorf("remove %s: %w", dir, err)
		}
		removed = append(removed, dir)
	}
	return removed, nil
}

// SystemClock is the real Clock.
type SystemClock struct{}

// Now implements executor.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// AfterFunc implements executor.Clock.
func (SystemClock) AfterFunc(d time.Duration, f func()) executor.Timer { return time.AfterFunc(d, f) }
