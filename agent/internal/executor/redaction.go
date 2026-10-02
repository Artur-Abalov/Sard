// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor

import (
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/redact"
	"github.com/Artur-Abalov/sard/agent/internal/steplog"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// prepare compiles the step's secrets and opens its tool output (A7c).
// Both are in place before the handler starts, also when it fails: the
// error then becomes the step's result and the handler never runs.
func (e *Executor) prepare(c *command) error {
	set, refs, err := e.compile(c)
	out := steplog.New(set, func(line string) {
		e.opts.Sink.Log(c.step.GetCommandId(), &agentv1.LogLine{
			Time:  timestamppb.New(e.opts.Clock.Now()),
			Level: e.opts.OutputLevel(line),
			Text:  line,
		})
	})
	e.mu.Lock()
	c.mask, c.refs, c.out = set, refs, out
	e.mu.Unlock()
	return err
}

// compile builds the matcher of the step's secrets (nil when there are none)
// and the contents the plugin may ask for.
func (e *Executor) compile(c *command) (*redact.Set, map[string][]byte, error) {
	if e.opts.Secrets == nil {
		return nil, nil, nil
	}
	secrets, err := e.opts.Secrets.For(c.step)
	if err != nil {
		return nil, nil, fmt.Errorf("log redaction: %w", err)
	}
	var values [][]byte
	refs := map[string][]byte{}
	for _, s := range secrets {
		if len(s.Value) > 0 { // an empty value cannot leak
			values = append(values, s.Value)
		}
		if s.Ref != "" {
			refs[s.Ref] = s.Content
		}
	}
	// Without empty values Compile cannot fail.
	set, _ := redact.Compile(values)
	return set, refs, nil
}

// maskResult masks the texts of a result that may quote a tool or a plugin.
func maskResult(set *redact.Set, r *agentv1.StepResult) {
	r.Message = set.Mask(r.GetMessage())
	for _, check := range r.GetVerify().GetChecks() {
		check.Detail = set.Mask(check.GetDetail())
	}
}
