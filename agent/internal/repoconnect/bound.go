// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Clock is the part of a clock the connection needs; tests drive it by hand.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

// errConnectTimeout is the cause the first access is cancelled with when
// --connect-timeout runs out.
var errConnectTimeout = errors.New("connect timeout")

// maxLog is how much of restic's stderr is kept: the last retry reason is
// in its tail.
const maxLog = 8 << 10

// Log keeps the tail of restic's stderr, to tell why a storage did not
// answer (Р33). It is written from the goroutine that copies the stream.
type Log struct {
	mu  sync.Mutex
	buf []byte
}

// Write implements io.Writer.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > maxLog {
		l.buf = l.buf[len(l.buf)-maxLog:]
	}
	return len(p), nil
}

// String is what was kept.
func (l *Log) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// Bound limits the first access to a storage, the call that finds out
// whether the repository is there (Р33). Zero Timeout: no limit.
type Bound struct {
	Clock   Clock
	Timeout time.Duration
}

// Inspect asks restic whether the repository is initialised, within the
// time Bound allows. When the time runs out restic is stopped and the
// failure is BACKEND_UNAVAILABLE with the last reason of a retry it
// printed, if any. Time the command spends elsewhere is not counted.
func (b Bound) Inspect(ctx context.Context, cli restic.Repository, target repoinit.Target, log *Log) (string, bool, *refusal.Failure) {
	if b.Timeout <= 0 {
		return repoinit.Inspect(ctx, cli, target)
	}
	bounded, cancel := b.limit(ctx)
	defer cancel(nil)
	id, initialized, f := repoinit.Inspect(bounded, cli, target)
	if f != nil && expired(ctx, bounded) {
		return "", false, b.unanswered(target, log)
	}
	return id, initialized, f
}

// limit is ctx, ended with errConnectTimeout when the time runs out.
func (b Bound) limit(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	bounded, cancel := context.WithCancelCause(ctx)
	timer := b.Clock.After(b.Timeout)
	go func() {
		select {
		case <-timer:
			cancel(errConnectTimeout)
		case <-bounded.Done():
		}
	}()
	return bounded, cancel
}

// expired: the limited context ended because of the limit, not because
// the command itself was stopped.
func expired(ctx, bounded context.Context) bool {
	return ctx.Err() == nil && errors.Is(context.Cause(bounded), errConnectTimeout)
}

// unanswered is the failure of a storage that did not answer in time.
func (b Bound) unanswered(target repoinit.Target, log *Log) *refusal.Failure {
	f := refusal.Fail(refusal.BackendUnavailable, "the storage at %s did not answer within %s (--connect-timeout)", target.Where, b.Timeout)
	if reason := target.Scrub(repoinit.RetryReason(log.String())); reason != "" {
		f.Detail += ": " + reason
	}
	f.Detail += "; the command can be repeated"
	return f
}
