// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"slices"
	"sync"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// maxChunkLines caps the lines of one LogChunk.
const maxChunkLines = 64

// rememberedAcks bounds the acked ids kept to refuse a stale resend.
const rememberedAcks = 1024

// outbox holds what the agent has to send; the stream's single sender
// takes from it. Results stay until the server acks them and are offered
// once per stream; progress keeps only the latest report per command;
// logs are a bounded queue that makes writers wait when full.
type outbox struct {
	mu    sync.Mutex
	space *sync.Cond // signalled when logs shrink or the outbox closes
	wake  chan struct{}

	results  []*agentv1.StepResult // unacked, in arrival order
	sent     map[string]bool       // results already sent on this stream
	acked    []string              // recent acks, oldest first
	progress []*agentv1.StepProgress
	logs     []logEntry
	logCap   int
	closed   bool
}

type logEntry struct {
	commandID string
	line      *agentv1.LogLine
}

func newOutbox(logCap int) *outbox {
	o := &outbox{wake: make(chan struct{}, 1), sent: map[string]bool{}, logCap: logCap}
	o.space = sync.NewCond(&o.mu)
	return o
}

// ready is signalled after every write.
func (o *outbox) ready() <-chan struct{} { return o.wake }

func (o *outbox) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Progress replaces the command's previous report; it never waits.
func (o *outbox) Progress(p *agentv1.StepProgress) {
	o.mu.Lock()
	defer o.mu.Unlock()
	i := slices.IndexFunc(o.progress, func(q *agentv1.StepProgress) bool { return q.GetCommandId() == p.GetCommandId() })
	if i >= 0 {
		o.progress[i] = p
	} else {
		o.progress = append(o.progress, p)
	}
	o.signal()
}

// Result keeps r until acked; it never waits.
func (o *outbox) Result(r *agentv1.StepResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.addResult(r)
	o.signal()
}

func (o *outbox) addResult(r *agentv1.StepResult) {
	id := r.GetCommandId()
	if slices.Contains(o.acked, id) || slices.ContainsFunc(o.results, func(q *agentv1.StepResult) bool { return q.GetCommandId() == id }) {
		return
	}
	o.results = append(o.results, r)
}

// Log queues a line, waiting while the queue is full (back pressure).
// After close the line is dropped: the agent is shutting down.
func (o *outbox) Log(commandID string, line *agentv1.LogLine) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for len(o.logs) >= o.logCap && !o.closed {
		o.space.Wait()
	}
	if o.closed {
		return
	}
	o.logs = append(o.logs, logEntry{commandID, line})
	o.signal()
}

// startStream makes every unacked result due again and adds the ones the
// executor still holds.
func (o *outbox) startStream(pending []*agentv1.StepResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	clear(o.sent)
	for _, r := range pending {
		o.addResult(r)
	}
	o.signal()
}

// ack drops a result for good.
func (o *outbox) ack(commandID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.results = slices.DeleteFunc(o.results, func(r *agentv1.StepResult) bool { return r.GetCommandId() == commandID })
	delete(o.sent, commandID)
	o.acked = append(o.acked, commandID)
	if len(o.acked) > rememberedAcks {
		o.acked = o.acked[1:]
	}
}

// next takes the next message for the stream: an unsent result, else the
// oldest progress, else a chunk of one command's logs.
func (o *outbox) next() (*agentv1.ConnectRequest, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.results {
		if !o.sent[r.GetCommandId()] {
			o.sent[r.GetCommandId()] = true
			return &agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_StepResult{StepResult: r}}, true
		}
	}
	if len(o.progress) > 0 {
		p := o.progress[0]
		o.progress = o.progress[1:]
		return &agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_StepProgress{StepProgress: p}}, true
	}
	if len(o.logs) > 0 {
		return o.takeChunk(), true
	}
	return nil, false
}

// takeChunk removes the leading run of one command's lines.
func (o *outbox) takeChunk() *agentv1.ConnectRequest {
	id := o.logs[0].commandID
	n := 1
	for n < len(o.logs) && n < maxChunkLines && o.logs[n].commandID == id {
		n++
	}
	chunk := &agentv1.LogChunk{CommandId: id}
	for _, e := range o.logs[:n] {
		chunk.Lines = append(chunk.Lines, e.line)
	}
	o.logs = o.logs[n:]
	o.space.Broadcast()
	return &agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_LogChunk{LogChunk: chunk}}
}

// requeue puts the lines of a chunk that failed to send back in front, so
// logs are not lost with a broken stream. Results come back with the next
// stream anyway; progress is superseded by the next report.
func (o *outbox) requeue(msg *agentv1.ConnectRequest) {
	chunk := msg.GetLogChunk()
	if chunk == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	back := make([]logEntry, 0, len(chunk.GetLines())+len(o.logs))
	for _, l := range chunk.GetLines() {
		back = append(back, logEntry{chunk.GetCommandId(), l})
	}
	o.logs = append(back, o.logs...)
}

// close releases writers waiting in Log.
func (o *outbox) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	o.space.Broadcast()
}
