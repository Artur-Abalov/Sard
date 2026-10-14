// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// The stream-level behaviour that a real connection cannot reach on cue:
// a Send that fails, a stream that cannot be opened. The tests hand the
// transport a scripted stream instead of a socket.

var errBroken = errors.New("broken stream")

// scriptedStream is a Connect stream whose Send and Recv the test writes.
type scriptedStream struct {
	grpc.ClientStream // unused: the transport only calls Send and Recv
	send              func(*agentv1.ConnectRequest) error
	recv              func() (*agentv1.ConnectResponse, error)
}

func (s *scriptedStream) Send(m *agentv1.ConnectRequest) error    { return s.send(m) }
func (s *scriptedStream) Recv() (*agentv1.ConnectResponse, error) { return s.recv() }

// scriptedClient opens the stream the test gives it.
type scriptedClient struct {
	agentv1.AgentServiceClient
	connect func(ctx context.Context) (agentv1.AgentService_ConnectClient, error)
}

func (c scriptedClient) Connect(ctx context.Context, _ ...grpc.CallOption) (agentv1.AgentService_ConnectClient, error) {
	return c.connect(ctx)
}

func serveWith(ctx context.Context, t *testing.T, r *rig, connect func(context.Context) (agentv1.AgentService_ConnectClient, error)) error {
	t.Helper()
	return r.tr.ServeForTest(ctx, scriptedClient{connect: connect}, beat)
}

func TestAStreamThatCannotBeOpenedEndsTheConnection(t *testing.T) {
	r := newRig(t)
	err := serveWith(context.Background(), t, r, func(context.Context) (agentv1.AgentService_ConnectClient, error) {
		return nil, status.Error(codes.Unavailable, "no stream")
	})
	if err == nil || !strings.HasPrefix(err.Error(), "stream: ") {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = serveWith(ctx, t, r, func(context.Context) (agentv1.AgentService_ConnectClient, error) {
		return nil, status.Error(codes.Canceled, "gone")
	})
	if err != context.Canceled {
		t.Fatalf("err = %v, want the context's error", err)
	}
}

// When Hello cannot be sent, the stream is over: nothing is said to have
// connected, and no result is offered.
func TestAFailedHelloEndsTheStreamAtOnce(t *testing.T) {
	r := newRig(t)
	stream := &scriptedStream{
		send: func(*agentv1.ConnectRequest) error { return errBroken },
		recv: func() (*agentv1.ConnectResponse, error) { return nil, io.EOF },
	}
	err := serveWith(context.Background(), t, r, func(context.Context) (agentv1.AgentService_ConnectClient, error) { return stream, nil })
	if !errors.Is(err, errBroken) || !strings.HasPrefix(err.Error(), "stream: ") {
		t.Fatalf("err = %v", err)
	}
	if log := r.logs.all(); strings.Contains(log, "connected to the server") || strings.Contains(log, "hello sent") {
		t.Fatalf("log:\n%s", log)
	}
}

// serve returns only when both of its goroutines are done, so nothing keeps
// using the stream after the connection is over.
func TestServeWaitsForTheReceiverToStop(t *testing.T) {
	r := newRig(t)
	r.tr.Result(&agentv1.StepResult{CommandId: "r1"})
	receiverSawCancel, release := make(chan struct{}), make(chan struct{})
	var streamCtx context.Context
	stream := &scriptedStream{
		send: func(m *agentv1.ConnectRequest) error {
			if m.GetStepResult() != nil {
				return errBroken // the sender fails first
			}
			return nil
		},
		recv: func() (*agentv1.ConnectResponse, error) {
			<-streamCtx.Done()
			close(receiverSawCancel)
			<-release
			return nil, streamCtx.Err()
		},
	}
	served := make(chan error, 1)
	go func() {
		served <- serveWith(context.Background(), t, r, func(ctx context.Context) (agentv1.AgentService_ConnectClient, error) {
			streamCtx = ctx
			return stream, nil
		})
	}()
	<-receiverSawCancel
	select {
	case <-served:
		t.Fatal("serve returned while the receiver was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := wait(t, served); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

// A heartbeat that cannot be sent ends the sender; it does not wait for
// the next wake-up.
func TestAFailedHeartbeatEndsTheSender(t *testing.T) {
	r := newRig(t)
	r.tr.Result(&agentv1.StepResult{CommandId: "r1"})
	release := make(chan struct{})
	stream := &scriptedStream{send: func(m *agentv1.ConnectRequest) error {
		if m.GetHeartbeat() != nil {
			return errBroken
		}
		<-release // the result: still being sent while the heartbeat falls due
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.tr.SendForTest(ctx, stream, beat) }()
	if d := r.clock.waitTimer(t); d != beat {
		t.Fatalf("heartbeat timer = %v", d)
	}
	r.clock.Advance(beat)
	close(release)
	if err := wait(t, done); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
}

// A log chunk that could not be sent is not lost: the next stream sends it.
func TestALogChunkThatFailedToSendGoesOutOnTheNextStream(t *testing.T) {
	r := newRig(t)
	r.tr.Log("c1", &agentv1.LogLine{Text: "a"})
	r.tr.Log("c1", &agentv1.LogLine{Text: "b"})
	broken := &scriptedStream{send: func(*agentv1.ConnectRequest) error { return errBroken }}
	if err := wait(t, sendInBackground(context.Background(), r, broken)); !errors.Is(err, errBroken) {
		t.Fatalf("err = %v", err)
	}
	sent := make(chan *agentv1.ConnectRequest, 4)
	healthy := &scriptedStream{send: func(m *agentv1.ConnectRequest) error { sent <- m; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := sendInBackground(ctx, r, healthy)
	select {
	case m := <-sent:
		lines := m.GetLogChunk().GetLines()
		if len(lines) != 2 || lines[0].GetText() != "a" || lines[1].GetText() != "b" {
			t.Fatalf("message = %v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the log lines were lost")
	}
	cancel()
	_ = wait(t, done)
}

func sendInBackground(ctx context.Context, r *rig, stream *scriptedStream) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.tr.SendForTest(ctx, stream, beat) }()
	return done
}
