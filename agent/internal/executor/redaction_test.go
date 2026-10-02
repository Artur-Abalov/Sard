// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"encoding/base64"
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

func TestAPanicValueIsMaskedInTheAgentLogToo(t *testing.T) {
	f, _ := masked(t)
	startMasked(t, f, "c1").panic("boom " + secretValue)
	f.sink.result(t)
	log := f.log.String()
	if !strings.Contains(log, "boom [REDACTED]") || strings.Contains(log, secretValue) {
		t.Fatalf("log:\n%s", log)
	}
}

func TestAStepThatPrintsSecretsOnEveryPathLeavesNoValueInTheAgentLog(t *testing.T) {
	const env = "ENV-MARKER-value"
	_, tune := withSecrets(executor.Secret{Name: "db", Value: []byte(secretValue)}, executor.Secret{Name: "env", Value: []byte(env)})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "token "+secretValue)
	writeOutput(t, c.r.Output(), "key "+secretValue+"\n")
	c.finish(nil, errors.New("failed "+env))
	wantLog(t, f.sink, "c1", info, "token [REDACTED]")
	wantLog(t, f.sink, "c1", info, "key [REDACTED]")
	wantResult(t, f.sink.result(t), "c1", failed, "failed [REDACTED]")
	if log := f.log.String(); strings.Contains(log, secretValue) || strings.Contains(log, env) {
		t.Fatalf("log:\n%s", log)
	}
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

// Short values are warned about by stepsecrets, once, not by every step.
func TestAShortSecretIsMaskedAndNotWarnedAboutByTheExecutor(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "pin", Value: []byte("471")}, executor.Secret{Name: "db", Value: []byte(secretValue)})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "pin 471")
	wantLog(t, f.sink, "c1", info, "pin [REDACTED]")
	if log := f.log.String(); strings.Contains(log, "471") || strings.Contains(log, "level=WARN") && strings.Contains(log, "short") {
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

// OQ-122: Host.Secret answers from the read that built the masker.
func TestTheHandlerGetsTheContentTheMaskerWasBuiltFrom(t *testing.T) {
	_, tune := withSecrets(
		executor.Secret{Name: "secret tok", Ref: "tok", Value: []byte(secretValue), Content: []byte(secretValue + "\n")},
		executor.Secret{Name: "url of repository", Value: []byte("url-pass")},
	)
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	got, ok := c.r.Secret("tok")
	if !ok || string(got) != secretValue+"\n" {
		t.Fatalf("Secret(tok) = %q, %v", got, ok)
	}
	got[0] = 'X' // a plugin may clear what it got
	if again, _ := c.r.Secret("tok"); string(again) != secretValue+"\n" {
		t.Fatalf("the executor's copy changed: %q", again)
	}
	if v, ok := c.r.Secret("url of repository"); ok {
		t.Fatalf("a value without a Ref is not offered: %q", v)
	}
	if v, ok := c.r.Secret("unknown"); ok {
		t.Fatalf("Secret(unknown) = %q", v)
	}
}

// --- A7b scenarios about the shape of the masked text

const keyPEM = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\nBKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----"

func keyMasked(t *testing.T) *fixture {
	t.Helper()
	_, tune := withSecrets(executor.Secret{Name: "pem", Value: []byte(keyPEM)}, executor.Secret{Name: "db", Value: []byte(secretValue)})
	return setup(t, tune)
}

func TestAMultilineKeyInToolOutputBecomesOneMarkerInOneLine(t *testing.T) {
	f := keyMasked(t)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "before\nkey "+keyPEM+" end\nafter\n")
	wantLog(t, f.sink, "c1", info, "before")
	wantLog(t, f.sink, "c1", info, "key [REDACTED] end")
	wantLog(t, f.sink, "c1", info, "after")
}

func TestAKeyFollowedByANewlineLeavesTheNewlineOutOfTheValue(t *testing.T) {
	f := keyMasked(t)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), keyPEM+"\ndone\n")
	wantLog(t, f.sink, "c1", info, "[REDACTED]")
	wantLog(t, f.sink, "c1", info, "done")
}

func TestOneLineOfAKeyPrintedAloneIsNotMasked(t *testing.T) {
	f := keyMasked(t)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "BKcwggSjAgEAAoIBAQC7")
	wantLog(t, f.sink, "c1", info, "BKcwggSjAgEAAoIBAQC7")
}

func TestASecretLongerThanALogLineIsOneMarker(t *testing.T) {
	big := strings.Repeat("q", 20000)
	_, tune := withSecrets(executor.Secret{Name: "big", Value: []byte(big)})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "x"+big+"y\n")
	wantLog(t, f.sink, "c1", info, "x[REDACTED]y")
}

func TestAShortSecretIsMaskedEverywhere(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "short", Value: []byte("ab7")})
	f := setup(t, tune)
	c := startMasked(t, f, "c1")
	writeOutput(t, c.r.Output(), "xab7y ab7\n")
	wantLog(t, f.sink, "c1", info, "x[REDACTED]y [REDACTED]")
}

func TestLinesWithoutSecretsArriveUnchangedAndInOrder(t *testing.T) {
	f, _ := masked(t)
	step := startMasked(t, f, "c1")
	var in strings.Builder
	for i := range 100 {
		if i%10 == 0 {
			in.WriteString("line " + secretValue + "\n")
			continue
		}
		in.WriteString(`line {"a":"QUJDREVGRw=="} ` + strings.Repeat("z", i) + "\n")
	}
	writeOutput(t, step.r.Output(), in.String())
	for i := range 100 {
		want := `line {"a":"QUJDREVGRw=="} ` + strings.Repeat("z", i)
		if i%10 == 0 {
			want = "line [REDACTED]"
		}
		wantLog(t, f.sink, "c1", info, want)
	}
}

func TestATextLookingLikeTheMarkerOrAlmostTheSecretIsLeftAlone(t *testing.T) {
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "value [REDACTED] already")
	wantLog(t, f.sink, "c1", info, "value [REDACTED] already")
	almost := secretValue[:len(secretValue)-1] + "X"
	c.r.Log(info, almost)
	wantLog(t, f.sink, "c1", info, almost)
}

func TestStepsRunningInParallelAreMaskedApartFromEachOther(t *testing.T) {
	_, tune := withSecrets(executor.Secret{Name: "pem", Value: []byte(keyPEM)}, executor.Secret{Name: "db", Value: []byte(secretValue)})
	f := setup(t, func(o *executor.Options) { tune(o); o.MaxParallel = 2 })
	f.e.Submit(backup("a"))
	f.e.Submit(backup("b"))
	first, second := f.files.next(t), f.files.next(t)
	byID := map[string]*call{first.step.GetCommandId(): first, second.step.GetCommandId(): second}
	writeOutput(t, byID["a"].r.Output(), "A1 "+secretValue+"\nA2\n")
	writeOutput(t, byID["b"].r.Output(), "B1\nB2 "+keyPEM+"\n")
	byID["a"].finish(snapshot("s1"), nil)
	byID["b"].finish(snapshot("s2"), nil)
	logs := map[string][]string{}
	for results := 0; results < 2; {
		e := f.sink.next(t)
		if e.log != nil {
			logs[e.logID] = append(logs[e.logID], e.log.GetText())
		}
		if e.result != nil {
			results++
		}
	}
	if got := strings.Join(logs["a"], "|"); got != "A1 [REDACTED]|A2" {
		t.Errorf("a = %q", got)
	}
	if got := strings.Join(logs["b"], "|"); got != "B1|B2 [REDACTED]" {
		t.Errorf("b = %q", got)
	}
}

func TestBase64OfASecretIsMaskedOnEveryPathOfAStepsText(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte(secretValue))
	f, _ := masked(t)
	c := startMasked(t, f, "c1")
	c.r.Log(info, "auth "+enc)
	wantLog(t, f.sink, "c1", info, "auth [REDACTED]")
	writeOutput(t, c.r.Output(), "Authorization: "+enc+"\n")
	wantLog(t, f.sink, "c1", info, "Authorization: [REDACTED]")
	out := &agentv1.StepResult{Output: &agentv1.StepResult_Verify{Verify: &agentv1.VerifyOutput{
		Checks: []*agentv1.CheckResult{{Name: "files", Detail: "got " + enc}},
	}}}
	c.finish(out, errors.New("failed "+enc))
	r := f.sink.result(t)
	if r.GetMessage() != "failed [REDACTED]" || r.GetVerify().GetChecks()[0].GetDetail() != "got [REDACTED]" {
		t.Fatalf("result = %v", r)
	}
}
