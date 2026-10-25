// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"math"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// workVolume: the work volume executes nothing and has room for the copy,
// and the job gets a directory of its own there.
func (x *run) workVolume(context.Context) outcome {
	vol, err := x.opts.Disk.Volume(x.opts.WorkDir)
	if err != nil {
		return cannotRun("cannot measure the work volume %s: %v", x.opts.WorkDir, err)
	}
	if !vol.NoExec {
		return cannotRun("the work volume %s is not mounted noexec: a restored file could be executed", x.opts.WorkDir)
	}
	room := x.room(vol)
	if !room.proves() {
		return room
	}
	if err := x.opts.Disk.MakeDir(x.dir); err != nil {
		return cannotRun("cannot make %s: %v", x.dir, err)
	}
	return room
}

// room compares the free space with what the copy needs, when its size is known.
func (x *run) room(vol Volume) outcome {
	sizer, ok := x.plugin.(sdk.CopySizer)
	var size uint64
	if ok && x.reference != nil {
		size, ok = sizer.CopySize(*x.reference)
	}
	if !ok || x.reference == nil {
		return passed("the size of the copy is unknown, no room was reserved; %d bytes free", vol.Free)
	}
	need := needed(size, x.opts.Space)
	if vol.Free < need {
		return cannotRun("the copy needs %d bytes free on %s, %d are", need, x.opts.WorkDir, vol.Free)
	}
	return passed("the copy needs %d bytes, %d are free", need, vol.Free)
}

// needed is size times the factor plus the headroom, at most the largest number.
func needed(size uint64, s Space) uint64 {
	scaled := float64(size) * s.Factor
	if scaled >= math.MaxUint64-float64(s.Headroom) {
		return math.MaxUint64
	}
	return uint64(scaled) + s.Headroom
}
