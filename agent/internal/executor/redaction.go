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
	set, err := e.compile(c)
	out := steplog.New(set, func(line string) {
		e.opts.Sink.Log(c.step.GetCommandId(), &agentv1.LogLine{
			Time:  timestamppb.New(e.opts.Clock.Now()),
			Level: e.opts.OutputLevel(line),
			Text:  line,
		})
	})
	e.mu.Lock()
	c.mask, c.out = set, out
	e.mu.Unlock()
	return err
}

// compile builds the matcher of the step's secrets; nil when there are none.
func (e *Executor) compile(c *command) (*redact.Set, error) {
	if e.opts.Secrets == nil {
		return nil, nil
	}
	secrets, err := e.opts.Secrets.For(c.step)
	if err != nil {
		return nil, fmt.Errorf("log redaction: %w", err)
	}
	var names []string
	var values [][]byte
	for _, s := range secrets {
		if len(s.Value) > 0 { // an empty value cannot leak
			names, values = append(names, s.Name), append(values, s.Value)
		}
	}
	// Without empty values Compile cannot fail.
	set, _ := redact.Compile(values, redact.OnShortValue(func(i, _ int) {
		e.opts.Logger.Warn("a short secret masks the same text anywhere in step logs",
			"command_id", c.step.GetCommandId(), "secret", names[i])
	}))
	return set, nil
}

// maskResult masks the texts of a result that may quote a tool or a plugin.
func maskResult(set *redact.Set, r *agentv1.StepResult) {
	r.Message = set.Mask(r.GetMessage())
	for _, check := range r.GetVerify().GetChecks() {
		check.Detail = set.Mask(check.GetDetail())
	}
}
