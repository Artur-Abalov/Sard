// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"strings"
	"testing"
)

func wantLogged(t *testing.T, f *fixture, lines ...string) {
	t.Helper()
	log := f.log.String()
	for _, want := range lines {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// Scenario: Приём, старт, завершение и повтор шага видны в журнале агента
func TestTheLogTellsAcceptStartFinishAndRepeats(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.ConfigJson = `{"paths":["/srv/very-private"]}`
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.e.Submit(backup("c1")) // the server sends it again while it runs
	f.sink.progress(t)
	c.finish(snapshot("snap-c1"), nil)
	f.sink.result(t)
	f.e.Submit(backup("c1"))
	f.sink.result(t)
	wantLogged(t, f,
		`level=INFO msg="step accepted" command_id=c1 plugin=files action=ACTION_BACKUP`,
		`level=INFO msg="repeated command" command_id=c1 answer=progress`,
		`level=INFO msg="step started" command_id=c1`,
		`level=INFO msg="step finished" command_id=c1 status=STEP_STATUS_SUCCEEDED`,
		`level=INFO msg="repeated command" command_id=c1 answer=result`,
	)
	if strings.Contains(f.log.String(), "very-private") {
		t.Fatalf("the step's config reached the log:\n%s", f.log)
	}
}

func TestARejectedStepIsLoggedAsFinished(t *testing.T) {
	f := setup(t, nil)
	s := backup("r1")
	s.Plugin = "tape"
	f.e.Submit(s)
	f.sink.result(t)
	wantLogged(t, f, `level=INFO msg="step finished" command_id=r1 status=STEP_STATUS_REJECTED`)
}

// Scenario: Прерванные при старте шаги перечислены в журнале агента
func TestInterruptedStepsAreCountedInTheLogAtStart(t *testing.T) {
	f := setup(t, nil)
	interruptMidStep(t, f)
	wantLogged(t, f, `level=INFO msg="interrupted steps reported as failed" count=2 command_ids="[c2 c3]"`)
}

func TestAStartWithoutInterruptedStepsSaysNothingAboutThem(t *testing.T) {
	f := setup(t, nil)
	f.restart(t)
	if strings.Contains(f.log.String(), "interrupted") {
		t.Fatalf("log:\n%s", f.log)
	}
}

func TestASingleInterruptedStepIsLoggedToo(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	wantLogged(t, f, `msg="interrupted steps reported as failed" count=1 command_ids=[c1]`)
}
