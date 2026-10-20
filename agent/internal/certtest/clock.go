// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certtest

import (
	"sync"
	"testing"
	"time"
)

// Clock is a clock that moves only when the test moves it. It satisfies
// transport.Clock.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []timer
	added  chan time.Duration
}

type timer struct {
	at time.Time
	ch chan time.Time
}

// NewClock starts a clock at now.
func NewClock(now time.Time) *Clock {
	return &Clock{now: now, added: make(chan time.Duration, 4096)}
}

// Now is the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After starts a timer that fires when the clock has moved by d.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, timer{at: c.now.Add(d), ch: ch})
	c.mu.Unlock()
	c.added <- d
	return ch
}

// Advance moves the clock by d and fires every timer that is due.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at.After(c.now) {
			kept = append(kept, tm)
			continue
		}
		tm.ch <- c.now
	}
	c.timers = kept
}

// Set moves the clock to at, which must not be earlier than now, and fires
// every timer that is due.
func (c *Clock) Set(at time.Time) { c.Advance(at.Sub(c.Now())) }

// WaitTimer returns the duration of the next timer started by After, waiting
// for it a few real seconds; it is how a test learns that the agent is idle.
func (c *Clock) WaitTimer(t testing.TB) time.Duration {
	t.Helper()
	select {
	case d := <-c.added:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no timer was started")
		return 0
	}
}

// WaitTimerOf is WaitTimer that skips timers of other durations (heartbeats).
func (c *Clock) WaitTimerOf(t testing.TB, skip time.Duration) time.Duration {
	t.Helper()
	for {
		if d := c.WaitTimer(t); d != skip {
			return d
		}
	}
}
