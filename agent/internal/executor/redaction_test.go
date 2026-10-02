// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	secretValue = "hunter2-very-secret"
	info        = agentv1.LogLevel_LOG_LEVEL_INFO
	warn        = agentv1.LogLevel_LOG_LEVEL_WARN
)

// fakeSecrets answers every step with the same values and records the steps.
type fakeSecrets struct {
	values []executor.Secret
	err    error
	steps  chan *agentv1.RunStep
}

func (s *fakeSecrets) For(step *agentv1.RunStep) ([]executor.Secret, error) {
	s.steps <- step
	return s.values, s.err
}

func withSecrets(values ...executor.Secret) (*fakeSecrets, func(*executor.Options)) {
	s := &fakeSecrets{values: values, steps: make(chan *agentv1.RunStep, 100)}
	return s, func(o *executor.Options) {
		o.Secrets = s
		o.OutputLevel = func(line string) agentv1.LogLevel {
			if strings.HasPrefix(line, "Fatal:") {
				return warn
			}
			return info
		}
	}
}

func masked(t *testing.T) (*fixture, *fakeSecrets) {
	t.Helper()
	s, tune := withSecrets(executor.Secret{Name: "db", Value: []byte(secretValue)})
	return setup(t, tune), s
}

// wantLog takes the next event, which must be a log line of id.
func wantLog(t *testing.T, s *fakeSink, id string, level agentv1.LogLevel, text string) {
	t.Helper()
	e := s.next(t)
	if e.log == nil || e.logID != id || e.log.GetLevel() != level || e.log.GetText() != text || e.log.GetTime() == nil {
		t.Fatalf("event = %+v, want log %s %v %q", e, id, level, text)
	}
}

func writeOutput(t *testing.T, w io.Writer, chunks ...string) {
	t.Helper()
	for _, c := range chunks {
		if n, err := w.Write([]byte(c)); n != len(c) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", c, n, err)
		}
	}
}

func startMasked(t *testing.T, f *fixture, id string) *call {
	t.Helper()
	f.e.Submit(backup(id))
	accepted(t, f.sink, id)
	return f.files.next(t)
}

// --- 1. every source of a step's text

func TestThePluginsLogLinesAreMasked(t *testing.T) {
	f, secrets := masked(t)
	c := startMasked(t, f, "c1")
	if got := <-secrets.steps; got.GetCommandId() != "c1" {
		t.Fatalf("values asked for %v", got)
	}
	c.r.Log(info, "connecting with "+secretValue+" now")
	wantLog(t, f.sink, "c1", info, "connecting with [REDACTED] now")
	c.r.Log(warn, "nothing to hide")
	wantLog(t, f.sink, "c1", warn, "nothing to hide")
	c.r.Log(info, `{"password":"`+secretValue+`"}`)
	wantLog(t, f.sink, "c1", info, `{"password":"[REDACTED]"}`)
	c.finish(snapshot("s1"), nil)
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
}

func TestToolOutputIsMaskedAcrossWritesAndCutIntoWholeLines(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	out := c.r.Output()
	writeOutput(t, out, "Fatal: wrong password hun", "ter2-very-se", "cret\nplain line\npartial")
	wantLog(t, f.sink, "c1", warn, "Fatal: wrong password [REDACTED]")
	wantLog(t, f.sink, "c1", info, "plain line")
	f.sink.quiet(t)
	c.finish(snapshot("s1"), nil)
	wantLog(t, f.sink, "c1", info, "partial") // the last line precedes the result
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
}

func TestASecretSplitAcrossLogChunksNeverReachesTheSink(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	in := "a " + secretValue + " b\n"
	for i := range len(in) {
		writeOutput(t, c.r.Output(), in[i:i+1])
	}
	c.finish(snapshot("s1"), nil)
	wantLog(t, f.sink, "c1", info, "a [REDACTED] b")
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
}

func TestTheResultMessageIsMaskedBeforeItIsSentOrStored(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	c.finish(nil, errors.New("restic backup: exit code 1: Fatal: s3://u:"+secretValue+"@host"))
	want := "restic backup: exit code 1: Fatal: s3://u:[REDACTED]@host"
	wantResult(t, f.sink.result(t), "c1", failed, want)
	f.restart(t)
	pending := f.e.PendingResults()
	if len(pending) != 1 || pending[0].GetMessage() != want {
		t.Fatalf("stored = %v", pending)
	}
}

func TestAPanicValueIsMasked(t *testing.T) {
	f, _ := masked(t)
	startMasked(t, f, "c1").panic("bad key " + secretValue)
	wantResult(t, f.sink.result(t), "c1", failed, "plugin panicked: bad key [REDACTED]")
}

func TestVerifyCheckDetailsAreMasked(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	out := &agentv1.StepResult{Output: &agentv1.StepResult_Verify{Verify: &agentv1.VerifyOutput{
		Checks: []*agentv1.CheckResult{{Name: "files", Detail: "token " + secretValue + " differs"}},
	}}}
	c.finish(out, errors.New("check failed"))
	r := f.sink.result(t)
	if got := r.GetVerify().GetChecks()[0].GetDetail(); got != "token [REDACTED] differs" {
		t.Fatalf("detail = %q", got)
	}
}

// --- 2. the held-back tail on every way a step ends

func TestTheTailIsReleasedMaskedWhenTheStepIsCancelled(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "done\nkey "+secretValue)
	wantLog(t, f.sink, "c1", info, "done")
	f.e.Cancel("c1")
	wantLog(t, f.sink, "c1", info, "key [REDACTED]")
	wantResult(t, f.sink.result(t), "c1", cancelled, "cancelled by the server")
}

func TestTheTailIsReleasedMaskedWhenTheStepTimesOut(t *testing.T) {
	f, _ := masked(t)
	s := backup("c1")
	s.Timeout = durationpb.New(time.Second)
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	writeOutput(t, c.r.Output(), "key "+secretValue[:5])
	f.clock.Advance(time.Second)
	wantLog(t, f.sink, "c1", info, "key "+secretValue[:5])
	wantResult(t, f.sink.result(t), "c1", timedOut, "exceeded the step timeout of 1s")
}

func TestTheTailIsReleasedMaskedWhenThePluginPanics(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "key "+secretValue)
	c.panic("boom")
	wantLog(t, f.sink, "c1", info, "key [REDACTED]")
	wantResult(t, f.sink.result(t), "c1", failed, "plugin panicked: boom")
}

func TestTheTailOfAGivenUpStepIsReleasedAndLaterOutputDropped(t *testing.T) {
	s, tune := withSecrets(executor.Secret{Name: "db", Value: []byte(secretValue)})
	f := setup(t, tune)
	step := backup("c1")
	step.Plugin = "stubborn"
	f.e.Submit(step)
	accepted(t, f.sink, "c1")
	<-s.steps
	c := f.stubborn.next(t)
	writeOutput(t, c.r.Output(), "key "+secretValue)
	f.e.Cancel("c1")
	f.clock.Advance(30 * time.Second)
	wantResult(t, f.sink.result(t), "c1", cancelled, "cancelled by the server; the plugin did not stop within 30s")
	wantLog(t, f.sink, "c1", info, "key [REDACTED]")
	writeOutput(t, c.r.Output(), "late "+secretValue+"\n")
	f.sink.quiet(t)
}

// --- the values themselves

func TestAStepWhoseSecretsCannotBeReadFailsWithoutStarting(t *testing.T) {
	s, tune := withSecrets()
	s.err = errors.New(`cannot read secret "db": permission denied`)
	f := setup(t, tune)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	wantResult(t, f.sink.result(t), "c1", failed, `log redaction: cannot read secret "db": permission denied`)
	f.files.idle(t)
	if f.files.runs.Load() != 0 {
		t.Fatal("the handler ran")
	}
}

func TestWithoutValuesLinesPassUnchanged(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "empty"})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "as is")
	wantLog(t, f.sink, "c1", info, "as is")
	writeOutput(t, c.r.Output(), "raw\n")
	wantLog(t, f.sink, "c1", info, "raw")
	c.finish(nil, errors.New("plain error"))
	wantResult(t, f.sink.result(t), "c1", failed, "plain error")
}

func TestWithoutASecretsSourceOutputIsLoggedAtInfo(t *testing.T) {
	f := setup(t, nil)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "Fatal: x\n")
	wantLog(t, f.sink, "c1", info, "Fatal: x")
}

func TestAShortSecretIsReportedByNameNeverByValue(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "pin", Value: []byte("4711")}, executor.Secret{Name: "db", Value: []byte(secretValue)})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "pin 4711")
	wantLog(t, f.sink, "c1", info, "pin [REDACTED]")
	log := f.log.String()
	if !strings.Contains(log, "secret=pin") || strings.Contains(log, "4711") || strings.Contains(log, "secret=db") {
		t.Fatalf("log:\n%s", log)
	}
}

// An empty value is skipped; the others are still masked, however short.
func TestAnEmptySecretDoesNotDisableTheOthers(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "empty"}, executor.Secret{Name: "one", Value: []byte("Z")})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "aZb")
	wantLog(t, f.sink, "c1", info, "a[REDACTED]b")
}
