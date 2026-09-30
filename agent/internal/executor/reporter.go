// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor

import (
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// reporter is the Reporter of one running command. Calls after the command
// finished (a late or abandoned handler) are dropped.
type reporter struct {
	e *Executor
	c *command
}

// Progress forwards at most one update per ProgressInterval; a new phase always passes.
func (r *reporter) Progress(phase agentv1.StepPhase, processed, total uint64) {
	r.ProgressFiles(phase, processed, total, 0, 0)
}

// ProgressFiles is Progress with file counters; both share the rate limit.
func (r *reporter) ProgressFiles(phase agentv1.StepPhase, processed, total, files, filesTotal uint64) {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	if !r.c.live() || phase == agentv1.StepPhase_STEP_PHASE_ACCEPTED || phase == agentv1.StepPhase_STEP_PHASE_UNSPECIFIED {
		return
	}
	now := r.e.opts.Clock.Now()
	if phase == r.c.progress.GetPhase() && now.Sub(r.c.sentAt) < r.e.opts.ProgressInterval {
		return
	}
	r.e.send(r.c, now, phase, counters{processed, total, files, filesTotal})
}

// Log forwards a line outside the executor's lock: the sink may block for
// back pressure (the transport's log queue), and that must slow down only this
// plugin, never Submit, Cancel or the RunningIDs of the next Hello.
func (r *reporter) Log(level agentv1.LogLevel, text string) {
	r.e.mu.Lock()
	live := r.c.live()
	now := r.e.opts.Clock.Now()
	r.e.mu.Unlock()
	if !live {
		return
	}
	r.e.opts.Sink.Log(r.c.step.GetCommandId(), &agentv1.LogLine{Time: timestamppb.New(now), Level: level, Text: text})
}
