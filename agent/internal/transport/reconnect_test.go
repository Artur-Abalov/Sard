// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

const beat = 30 * time.Second // the fake server's heartbeat interval

// Every broken stream is followed by a longer wait, up to a minute, and a
// new stream that opens with Hello.
func TestReconnectsWithGrowingDelay(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	for _, want := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		ss := r.server.next(t)
		if ss.read(t).GetHello() == nil {
			t.Fatal("stream did not open with Hello")
		}
		close(ss.done)
		if d := r.backoff(t, beat); d != want*time.Second {
			t.Fatalf("delay = %v, want %v", d, want*time.Second)
		}
		r.clock.Advance(want * time.Second)
	}
	r.server.next(t) // the ninth stream
	if n := r.server.registerCount(); n != 9 {
		t.Fatalf("Register calls = %d, want one per stream", n)
	}
}

// A stream that lived 30 s resets the delay; a refused Register counts as a
// broken connection.
func TestDelayResetsAfterAHealthyStream(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	close(ss.done)
	r.clock.Advance(r.backoff(t, beat)) // 1 s
	ss = r.server.next(t)
	ss.read(t)
	close(ss.done)
	if d := r.backoff(t, beat); d != 2*time.Second {
		t.Fatalf("delay = %v", d)
	}
	r.clock.Advance(2 * time.Second)
	ss = r.server.next(t)
	ss.read(t)
	r.clock.Advance(transport.HealthyStream)
	close(ss.done)
	if d := r.backoff(t, beat); d != time.Second {
		t.Fatalf("delay after a healthy stream = %v, want 1s", d)
	}
}

func TestUnavailableRegisterIsRetried(t *testing.T) {
	r := newRig(t)
	var calls int
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		calls++
		if calls == 1 {
			return nil, status.Error(codes.Unavailable, "starting")
		}
		return registered("a", beat), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	r.clock.Advance(r.backoff(t, beat))
	if r.server.next(t).read(t).GetHello() == nil {
		t.Fatal("no Hello after the retry")
	}
}

// Refusals that retrying cannot fix end Run at once.
func TestRunStopsOnPermanentRefusal(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		return nil, status.Error(codes.FailedPrecondition, "protocol 1 unsupported")
	}
	err := wait(t, r.run(context.Background()))
	if !errors.Is(err, transport.ErrIncompatibleProtocol) || r.server.registerCount() != 1 {
		t.Fatalf("err = %v after %d Register calls", err, r.server.registerCount())
	}
}

// Stopping the agent ends Run without an error, also while it waits to
// reconnect.
func TestRunStopsWithTheContext(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	close(ss.done)
	r.backoff(t, beat)
	cancel()
	if err := wait(t, done); err != nil {
		t.Fatalf("err = %v", err)
	}
	r2 := newRig(t)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := r2.run(ctx2)
	r2.server.next(t).read(t)
	cancel2()
	if err := wait(t, done2); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// Hello on every stream lists the commands running at that moment.
func TestHelloAfterReconnectCarriesTheCurrentCommands(t *testing.T) {
	r := newRig(t)
	r.state.running = []string{"a"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	r.state.mu.Lock()
	r.state.running = []string{"a", "b"}
	r.state.mu.Unlock()
	close(ss.done)
	r.clock.Advance(r.backoff(t, beat))
	if got := r.server.next(t).read(t).GetHello().GetRunningCommandIds(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("second Hello = %v", got)
	}
}

// Unacked results are sent again after a reconnect; acked ones never.
func TestOnlyUnackedResultsAreResent(t *testing.T) {
	r := newRig(t)
	r.state.pending = []*agentv1.StepResult{{CommandId: "r1"}, {CommandId: "r2"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	got := []string{ss.read(t).GetStepResult().GetCommandId(), ss.read(t).GetStepResult().GetCommandId()}
	if !slices.Equal(got, []string{"r1", "r2"}) {
		t.Fatalf("first stream results = %v", got)
	}
	ss.send <- &agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_ResultAck{ResultAck: &agentv1.ResultAck{CommandId: "r1"}}}
	if e := r.commands.event(t); e != "ack r1" {
		t.Fatalf("executor got %q", e)
	}
	r.tr.Result(&agentv1.StepResult{CommandId: "r3"}) // finishes while connected, never acked
	if id := ss.read(t).GetStepResult().GetCommandId(); id != "r3" {
		t.Fatalf("got %q, want r3", id)
	}
	close(ss.done)
	r.clock.Advance(r.backoff(t, beat))
	ss = r.server.next(t)
	ss.read(t)
	got = []string{ss.read(t).GetStepResult().GetCommandId(), ss.read(t).GetStepResult().GetCommandId()}
	if !slices.Equal(got, []string{"r2", "r3"}) {
		t.Fatalf("second stream results = %v", got)
	}
	select {
	case msg := <-ss.recv:
		t.Fatalf("unexpected %v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

// Progress, logs and results from many goroutines reach the server through
// the one sender; a full log queue slows writers but loses nothing.
func TestConcurrentReportsUnderBackPressure(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	const producers, lines = 10, 30
	var wg sync.WaitGroup
	for g := range producers {
		wg.Go(func() {
			id := fmt.Sprint("c", g)
			for i := range lines {
				r.tr.Progress(&agentv1.StepProgress{CommandId: id, BytesProcessed: uint64(i)})
				r.tr.Log(id, &agentv1.LogLine{Text: fmt.Sprint(i)})
			}
			r.tr.Result(&agentv1.StepResult{CommandId: id})
		})
	}
	results, logged := map[string]bool{}, 0
	for len(results) < producers || logged < producers*lines {
		msg := ss.read(t)
		if id := msg.GetStepResult().GetCommandId(); id != "" {
			results[id] = true
		}
		logged += len(msg.GetLogChunk().GetLines())
	}
	wg.Wait()
	if logged != producers*lines {
		t.Fatalf("log lines = %d, want %d", logged, producers*lines)
	}
}
