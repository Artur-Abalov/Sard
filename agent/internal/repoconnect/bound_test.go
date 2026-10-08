// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// hangingRepo never answers until its context ends.
type hangingRepo struct {
	restic.Repository
	started chan struct{}
	log     *repoconnect.Log
	line    string
}

func (h *hangingRepo) ID(ctx context.Context) (string, error) {
	_, _ = h.log.Write([]byte(h.line + "\n"))
	close(h.started)
	<-ctx.Done()
	return "", fmt.Errorf("restic cat: %w", ctx.Err())
}

// stepClock fires its timers when the test says so.
type stepClock struct {
	asked []time.Duration
	fire  chan time.Time
}

func (c *stepClock) After(d time.Duration) <-chan time.Time {
	c.asked = append(c.asked, d)
	return c.fire
}

var target = repoinit.Target{
	Name: "extra", Backend: "s3", Where: "s3:https://s3.example.com/bucket-b/extra", Remote: true,
	Scrub: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[x]") },
}

func TestTheFirstAccessThatDoesNotAnswerFailsWithTheReasonOfTheLastRetry(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	log := &repoconnect.Log{}
	repo := &hangingRepo{started: make(chan struct{}), log: log,
		line: "Load(<config/0000000000>, 0, 0) returned error, retrying after 1.2s: dial SECRET: connection refused"}
	done := make(chan *refusal.Failure, 1)
	go func() {
		_, _, f := repoconnect.Bound{Clock: clock, Timeout: 30 * time.Second}.Inspect(t.Context(), repo, target, log)
		done <- f
	}()
	<-repo.started
	clock.fire <- time.Now()
	f := <-done
	if f == nil || f.Reason != refusal.BackendUnavailable || f.Class != refusal.ClassTemporary {
		t.Fatalf("failure %+v", f)
	}
	for _, want := range []string{"did not answer within 30s", "--connect-timeout", "dial [x]: connection refused", target.Where} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail lacks %q: %s", want, f.Detail)
		}
	}
	if len(clock.asked) != 1 || clock.asked[0] != 30*time.Second {
		t.Fatalf("timers %v", clock.asked)
	}
}

func TestWithoutARetryReasonTheMessageStillSaysTheStorageDidNotAnswer(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	log := &repoconnect.Log{}
	repo := &hangingRepo{started: make(chan struct{}), log: log, line: "nothing to see"}
	done := make(chan *refusal.Failure, 1)
	go func() {
		_, _, f := repoconnect.Bound{Clock: clock, Timeout: time.Second}.Inspect(t.Context(), repo, target, log)
		done <- f
	}()
	<-repo.started
	clock.fire <- time.Now()
	f := <-done
	if f == nil || f.Detail != "the storage at "+target.Where+" did not answer within 1s (--connect-timeout); the command can be repeated" {
		t.Fatalf("failure %+v", f)
	}
}

func TestACancelledCommandIsNotAConnectTimeout(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	log := &repoconnect.Log{}
	repo := &hangingRepo{started: make(chan struct{}), log: log}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *refusal.Failure, 1)
	go func() {
		_, _, f := repoconnect.Bound{Clock: clock, Timeout: time.Minute}.Inspect(ctx, repo, target, log)
		done <- f
	}()
	<-repo.started
	cancel()
	if f := <-done; f == nil || f.Reason != refusal.Interrupted {
		t.Fatalf("failure %+v", f)
	}
}

func TestWithoutATimeoutTheClockIsNotAsked(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	repo := &fakeRepo{initialized: true, id: "ID-1"}
	repo.current = &repoconnect.Files{Password: "/nonexistent"}
	repo.password = ""
	id, ok, f := repoconnect.Bound{Clock: clock}.Inspect(t.Context(), repo, target, &repoconnect.Log{})
	if f != nil || !ok || id != "ID-1" || len(clock.asked) != 0 {
		t.Fatalf("%q %v %v timers %v", id, ok, f, clock.asked)
	}
}

func TestAnAnswerInTimeIsPassedOn(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	repo := &fakeRepo{initialized: true, id: "ID-1", current: &repoconnect.Files{Password: "/nonexistent"}}
	id, ok, f := repoconnect.Bound{Clock: clock, Timeout: time.Minute}.Inspect(t.Context(), repo, target, &repoconnect.Log{})
	if f != nil || !ok || id != "ID-1" {
		t.Fatalf("%q %v %v", id, ok, f)
	}
}

func TestTheLogKeepsTheTailOfWhatWasWritten(t *testing.T) {
	var log repoconnect.Log
	_, _ = log.Write([]byte(strings.Repeat("a", 6000)))
	_, _ = log.Write([]byte(strings.Repeat("b", 6000)))
	got := log.String()
	if len(got) != 8<<10 || !strings.HasSuffix(got, "b") || !strings.HasPrefix(got, "a") {
		t.Fatalf("kept %d bytes", len(got))
	}
}

func TestTheSmallestTimeoutIsStillALimit(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	repo := &fakeRepo{initialized: true, id: "ID-1", current: &repoconnect.Files{Password: "/nonexistent"}}
	_, _, _ = repoconnect.Bound{Clock: clock, Timeout: time.Nanosecond}.Inspect(t.Context(), repo, target, &repoconnect.Log{})
	if len(clock.asked) != 1 {
		t.Fatalf("timers %v", clock.asked)
	}
}

// answeringRepo answers once the limited context has ended, as a storage
// that was slow but did answer; and may wait for the test before it does.
type answeringRepo struct {
	restic.Repository
	seen    chan struct{}
	release chan struct{}
	err     error
}

func (a *answeringRepo) ID(ctx context.Context) (string, error) {
	<-ctx.Done()
	close(a.seen)
	<-a.release
	if a.err != nil {
		return "", a.err
	}
	return "ID-1", nil
}

func TestAnAnswerThatArrivesAfterTheLimitIsStillTheAnswer(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	repo := &answeringRepo{seen: make(chan struct{}), release: make(chan struct{})}
	type result struct {
		id string
		ok bool
		f  *refusal.Failure
	}
	done := make(chan result, 1)
	go func() {
		id, ok, f := repoconnect.Bound{Clock: clock, Timeout: time.Second}.Inspect(t.Context(), repo, target, &repoconnect.Log{})
		done <- result{id, ok, f}
	}()
	clock.fire <- time.Now()
	<-repo.seen
	close(repo.release)
	if r := <-done; r.f != nil || !r.ok || r.id != "ID-1" {
		t.Fatalf("%+v", r)
	}
}

func TestACommandStoppedAfterTheLimitFiredIsInterruptedNotUnanswered(t *testing.T) {
	clock := &stepClock{fire: make(chan time.Time, 1)}
	repo := &answeringRepo{seen: make(chan struct{}), release: make(chan struct{}), err: fmt.Errorf("restic cat: %w", context.Canceled)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *refusal.Failure, 1)
	go func() {
		_, _, f := repoconnect.Bound{Clock: clock, Timeout: time.Second}.Inspect(ctx, repo, target, &repoconnect.Log{})
		done <- f
	}()
	clock.fire <- time.Now()
	<-repo.seen
	cancel()
	close(repo.release)
	if f := <-done; f == nil || f.Reason != refusal.Interrupted {
		t.Fatalf("failure %+v", f)
	}
}
