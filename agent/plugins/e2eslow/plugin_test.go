// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package e2eslow_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/e2eslow"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// host is a fake sdk.Host that records progress.
type host struct{ progress [][2]uint64 }

func (h *host) Secret(name string) ([]byte, error) { return nil, &sdk.SecretError{Name: name} }
func (h *host) Progress(done, total uint64)        { h.progress = append(h.progress, [2]uint64{done, total}) }
func (h *host) Log(sdk.Level, string)              {}

// clock is a fake e2eslow.Clock: time moves only by Sleep and by work, and
// every Sleep is recorded.
type clock struct {
	now    time.Time
	work   time.Duration // added to now on every Now, as if writing took time
	sleeps []time.Duration
	err    error
}

func (c *clock) Now() time.Time {
	c.now = c.now.Add(c.work)
	return c.now
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return c.err
}

// expected is the stream by its definition: block i is
// SHA-256(seed big-endian || i big-endian), cut to size.
func expected(seed, size uint64) []byte {
	var out []byte
	for i := uint64(0); uint64(len(out)) < size; i++ {
		var in [16]byte
		binary.BigEndian.PutUint64(in[:8], seed)
		binary.BigEndian.PutUint64(in[8:], i)
		sum := sha256.Sum256(in[:])
		out = append(out, sum[:]...)
	}
	return out[:size]
}

func config(size, rate, chunk, seed int) sdk.Config {
	return []byte(fmt.Sprintf(`{"size": %d, "rate": %d, "chunk": %d, "seed": %d}`, size, rate, chunk, seed))
}

func stream(t *testing.T, c *clock, cfg sdk.Config) ([]byte, *host, error) {
	t.Helper()
	p := e2eslow.Plugin{AgentVersion: "1.2.3", Clock: c}
	h := &host{}
	d, err := p.Dump(context.Background(), h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	err = p.Stream(context.Background(), h, cfg, d, &w)
	return w.Bytes(), h, err
}

func TestNameVersionAndSchema(t *testing.T) {
	p := e2eslow.Plugin{AgentVersion: "1.2.3"}
	if p.Name() != "e2e-slow" || p.Version() != "1.2.3" {
		t.Errorf("Name, Version = %q, %q", p.Name(), p.Version())
	}
	if !bytes.Contains(p.ConfigSchema(), []byte(`"required": ["size", "rate", "chunk"]`)) {
		t.Errorf("schema:\n%s", p.ConfigSchema())
	}
}

func TestPrepareAcceptsAValidConfigAndRejectsAnUnreadableOne(t *testing.T) {
	p := e2eslow.Plugin{}
	if err := p.Prepare(context.Background(), &host{}, config(10, 10, 10, 0)); err != nil {
		t.Errorf("valid: %v", err)
	}
	if err := p.Prepare(context.Background(), &host{}, []byte(`{"size":"x"}`)); err == nil {
		t.Error("invalid: no error")
	}
}

func TestDumpIsOneStreamedFile(t *testing.T) {
	d, err := e2eslow.Plugin{}.Dump(context.Background(), &host{}, config(10, 10, 10, 0))
	if err != nil || d.Filename != e2eslow.Filename || !d.Streamed() || len(d.Paths) != 0 {
		t.Errorf("Dump = %+v, %v", d, err)
	}
	if e2eslow.Filename != "e2e-slow.bin" {
		t.Errorf("Filename = %q", e2eslow.Filename)
	}
	if _, err := (e2eslow.Plugin{}).Dump(context.Background(), &host{}, []byte(`[`)); err == nil {
		t.Error("invalid config: no error")
	}
}

func TestStreamWritesTheSeededBytesInChunksWithProgress(t *testing.T) {
	got, h, err := stream(t, &clock{}, config(100, 1000, 40, 7))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, expected(7, 100)) {
		t.Errorf("stream differs from SHA-256 counter blocks of seed 7")
	}
	want := [][2]uint64{{40, 100}, {80, 100}, {100, 100}}
	if !slices.Equal(h.progress, want) {
		t.Errorf("progress = %v, want %v", h.progress, want)
	}
}

// writes records the size of every Write.
type writes struct{ sizes []int }

func (w *writes) Write(p []byte) (int, error) {
	w.sizes = append(w.sizes, len(p))
	return len(p), nil
}

// One Write per chunk: restic reads a pipe, and a syscall per 32-byte hash
// block made a 1 MiB/s stream crawl in a container (T3s, first e2e run).
func TestStreamWritesEachChunkInOneWrite(t *testing.T) {
	w := &writes{}
	if err := (e2eslow.Plugin{Clock: &clock{}}).Stream(context.Background(), &host{}, config(100, 1000, 40, 0), sdk.Dump{}, w); err != nil {
		t.Fatal(err)
	}
	if want := []int{40, 40, 20}; !slices.Equal(w.sizes, want) {
		t.Errorf("writes = %v, want %v", w.sizes, want)
	}
}

func TestTheSeedSelectsTheData(t *testing.T) {
	a, _, _ := stream(t, &clock{}, config(64, 1000, 64, 1))
	b, _, _ := stream(t, &clock{}, config(64, 1000, 64, 2))
	if bytes.Equal(a, b) || !bytes.Equal(b, expected(2, 64)) {
		t.Error("seed 1 and 2 give the same bytes, or seed 2 is not its own stream")
	}
}

// 200 bytes at 100 B/s take 2 s: after each 50-byte chunk the plugin sleeps
// until the time the rate allows for the bytes written so far.
func TestStreamKeepsToTheRate(t *testing.T) {
	c := &clock{now: time.Unix(1000, 0)}
	if _, _, err := stream(t, c, config(200, 100, 50, 0)); err != nil {
		t.Fatal(err)
	}
	half := 500 * time.Millisecond
	if want := []time.Duration{half, half, half, half}; !slices.Equal(c.sleeps, want) {
		t.Errorf("sleeps = %v, want %v", c.sleeps, want)
	}
}

// Time spent writing counts against the budget: the plugin sleeps only
// for the rest, and not at all when writing is already slower.
func TestStreamSleepsOnlyForWhatWritingDidNotTake(t *testing.T) {
	c := &clock{work: 100 * time.Millisecond}
	if _, _, err := stream(t, c, config(200, 100, 50, 0)); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{400 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond}; !slices.Equal(c.sleeps, want) {
		t.Errorf("sleeps = %v, want %v", c.sleeps, want)
	}
	slow := &clock{work: time.Second}
	if _, _, err := stream(t, slow, config(200, 100, 50, 0)); err != nil || len(slow.sleeps) != 0 {
		t.Errorf("slow writer slept %v, err %v", slow.sleeps, err)
	}
}

// Writing that takes exactly the budget leaves nothing to sleep; a
// nanosecond less leaves that nanosecond per chunk written so far.
func TestStreamSleepsOnlyWhenTimeIsLeft(t *testing.T) {
	exact := &clock{work: 500 * time.Millisecond}
	if _, _, err := stream(t, exact, config(200, 100, 50, 0)); err != nil || len(exact.sleeps) != 0 {
		t.Errorf("exact budget slept %v, err %v", exact.sleeps, err)
	}
	early := &clock{work: 500*time.Millisecond - time.Nanosecond}
	if _, _, err := stream(t, early, config(200, 100, 50, 0)); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{1, 1, 1, 1}; !slices.Equal(early.sleeps, want) {
		t.Errorf("sleeps = %v, want %v", early.sleeps, want)
	}
}

func TestStreamStopsWhenSleepFails(t *testing.T) {
	stop := errors.New("cancelled")
	got, h, err := stream(t, &clock{err: stop}, config(200, 100, 50, 0))
	if !errors.Is(err, stop) || len(got) != 50 || len(h.progress) != 1 {
		t.Errorf("err %v after %d bytes, %d reports", err, len(got), len(h.progress))
	}
}

func TestStreamReturnsAtOnceWhenCtxIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var w bytes.Buffer
	err := e2eslow.Plugin{Clock: &clock{}}.Stream(ctx, &host{}, config(10, 10, 10, 0), sdk.Dump{}, &w)
	if !errors.Is(err, context.Canceled) || w.Len() != 0 {
		t.Errorf("err %v, wrote %d", err, w.Len())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestStreamReturnsTheWriterError(t *testing.T) {
	err := e2eslow.Plugin{Clock: &clock{}}.Stream(context.Background(), &host{}, config(10, 10, 10, 0), sdk.Dump{}, failingWriter{})
	if err == nil || err.Error() != "pipe closed" {
		t.Errorf("err = %v", err)
	}
	if err := (e2eslow.Plugin{}).Stream(context.Background(), &host{}, []byte(`[`), sdk.Dump{}, failingWriter{}); err == nil {
		t.Error("invalid config: no error")
	}
}

func TestRealClockSleepsAndStopsOnCtx(t *testing.T) {
	c := e2eslow.RealClock{}
	start := c.Now()
	if err := c.Sleep(context.Background(), 20*time.Millisecond); err != nil || time.Since(start) < 20*time.Millisecond {
		t.Errorf("Sleep: %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep on a done ctx = %v", err)
	}
}

// A nil Clock is the real one: a tiny stream still respects the rate.
func TestNilClockIsTheRealOne(t *testing.T) {
	var w bytes.Buffer
	start := time.Now()
	err := e2eslow.Plugin{}.Stream(context.Background(), &host{}, config(10, 200, 10, 0), sdk.Dump{}, &w)
	if err != nil || w.Len() != 10 || time.Since(start) < 50*time.Millisecond {
		t.Errorf("err %v, %d bytes in %v", err, w.Len(), time.Since(start))
	}
}
