// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func result(id string) *agentv1.StepResult { return &agentv1.StepResult{CommandId: id} }

func progress(id string, done uint64) *agentv1.StepProgress {
	return &agentv1.StepProgress{CommandId: id, BytesProcessed: done}
}

func line(text string) *agentv1.LogLine { return &agentv1.LogLine{Text: text} }

// drain takes everything the outbox has for the stream now.
func drain(o *outbox) []string {
	var got []string
	for {
		msg, ok := o.next()
		if !ok {
			return got
		}
		got = append(got, describe(msg))
	}
}

func describe(msg *agentv1.ConnectRequest) string {
	switch {
	case msg.GetStepResult() != nil:
		return "result " + msg.GetStepResult().GetCommandId()
	case msg.GetStepProgress() != nil:
		return fmt.Sprintf("progress %s %d", msg.GetStepProgress().GetCommandId(), msg.GetStepProgress().GetBytesProcessed())
	case msg.GetLogChunk() != nil:
		c := msg.GetLogChunk()
		s := "log " + c.GetCommandId()
		for _, l := range c.GetLines() {
			s += " " + l.GetText()
		}
		return s
	}
	return msg.String()
}

func equal(t *testing.T, got []string, want ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sent %q\nwant %q", got, want)
	}
}

// Results first, then the latest progress per command, then logs batched
// per command in arrival order.
func TestOutboxOrderAndCoalescing(t *testing.T) {
	o := newOutbox(8)
	o.Log("c1", line("a"))
	o.Progress(progress("c1", 10))
	o.Log("c1", line("b"))
	o.Log("c2", line("x"))
	o.Progress(progress("c2", 5))
	o.Progress(progress("c1", 20)) // replaces 10
	o.Result(result("c0"))
	equal(t, drain(o), "result c0", "progress c1 20", "progress c2 5", "log c1 a b", "log c2 x")
}

// A result is sent once per stream and again on every new stream until acked.
func TestOutboxKeepsResultsUntilAcked(t *testing.T) {
	o := newOutbox(8)
	o.startStream([]*agentv1.StepResult{result("r1"), result("r2")})
	o.Result(result("r2")) // the executor reports it again: still one result
	o.Result(result("r3"))
	equal(t, drain(o), "result r1", "result r2", "result r3")
	o.ack("r1")
	o.ack("unknown")
	equal(t, drain(o))
	o.startStream(nil)
	equal(t, drain(o), "result r2", "result r3")
	o.ack("r2")
	o.startStream([]*agentv1.StepResult{result("r3")})
	equal(t, drain(o), "result r3")
}

// An acked result the executor still lists is not resent.
func TestOutboxDoesNotResendAnAckedResult(t *testing.T) {
	o := newOutbox(8)
	o.Result(result("r1"))
	drain(o)
	o.ack("r1")
	o.startStream([]*agentv1.StepResult{result("r1")})
	equal(t, drain(o))
}

// Logs of a failed send go back to the front of the queue.
func TestOutboxRequeuesUnsentLogs(t *testing.T) {
	o := newOutbox(8)
	o.Log("c1", line("a"))
	o.Log("c1", line("b"))
	msg, _ := o.next()
	o.Log("c1", line("c"))
	o.requeue(msg)
	equal(t, drain(o), "log c1 a b c")
}

// Requeued progress and results are not logs: progress is superseded by
// the next report, results by the next stream.
func TestOutboxRequeueIgnoresOtherMessages(t *testing.T) {
	o := newOutbox(8)
	o.Progress(progress("c1", 1))
	msg, _ := o.next()
	o.requeue(msg)
	equal(t, drain(o))
}

// A full log queue makes Log wait (back pressure) instead of dropping lines.
func TestOutboxLogWaitsForSpace(t *testing.T) {
	o := newOutbox(2)
	o.Log("c1", line("1"))
	o.Log("c1", line("2"))
	written := make(chan struct{})
	go func() {
		o.Log("c1", line("3"))
		close(written)
	}()
	select {
	case <-written:
		t.Fatal("Log did not wait for space")
	case <-time.After(50 * time.Millisecond):
	}
	// One message only: draining would race with the Log it wakes up.
	msg, _ := o.next()
	equal(t, []string{describe(msg)}, "log c1 1 2")
	<-written
	equal(t, drain(o), "log c1 3")
}

// Progress and results never wait, even when logs are full.
func TestOutboxProgressAndResultsNeverWait(t *testing.T) {
	o := newOutbox(1)
	o.Log("c1", line("1"))
	done := make(chan struct{})
	go func() {
		o.Progress(progress("c1", 1))
		o.Result(result("c1"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Progress or Result blocked on a full log queue")
	}
}

// Closing releases writers waiting for space; later logs are dropped.
func TestOutboxCloseReleasesWaitingWriters(t *testing.T) {
	o := newOutbox(1)
	o.Log("c1", line("1"))
	released := make(chan struct{})
	go func() {
		o.Log("c1", line("2"))
		close(released)
	}()
	time.Sleep(10 * time.Millisecond)
	o.close()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("close did not release the writer")
	}
	o.Log("c1", line("3"))
	equal(t, drain(o), "log c1 1")
}

// Every write wakes a waiting sender.
func TestOutboxSignalsNewMessages(t *testing.T) {
	o := newOutbox(4)
	for name, write := range map[string]func(){
		"progress": func() { o.Progress(progress("c", 1)) },
		"result":   func() { o.Result(result("c")) },
		"log":      func() { o.Log("c", line("l")) },
	} {
		write()
		select {
		case <-o.ready():
		default:
			t.Errorf("%s did not signal", name)
		}
		drain(o)
	}
}

// A long run of one command's logs is split into chunks.
func TestOutboxSplitsLongLogRuns(t *testing.T) {
	o := newOutbox(2 * maxChunkLines)
	for i := range maxChunkLines + 1 {
		o.Log("c", line(fmt.Sprint(i)))
	}
	first, _ := o.next()
	second, _ := o.next()
	if len(first.GetLogChunk().GetLines()) != maxChunkLines || len(second.GetLogChunk().GetLines()) != 1 {
		t.Fatalf("chunks of %d and %d lines", len(first.GetLogChunk().GetLines()), len(second.GetLogChunk().GetLines()))
	}
}

// Many producers under -race; no result is lost while logs are saturated.
func TestOutboxConcurrentProducers(t *testing.T) {
	o := newOutbox(4)
	var wg sync.WaitGroup
	for g := range 20 {
		wg.Go(func() {
			id := fmt.Sprint("c", g)
			for i := range 50 {
				o.Progress(progress(id, uint64(i)))
				o.Log(id, line(fmt.Sprint(i)))
			}
			o.Result(result(id))
		})
	}
	results := map[string]bool{}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	for {
		collectResults(o, results)
		select {
		case <-finished:
			collectResults(o, results)
			if len(results) != 20 {
				t.Fatalf("results sent: %d of 20", len(results))
			}
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func collectResults(o *outbox, into map[string]bool) {
	for _, s := range drain(o) {
		if id, ok := strings.CutPrefix(s, "result "); ok {
			into[id] = true
		}
	}
}
