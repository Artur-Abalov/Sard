// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package e2eslow is a source plugin for the e2e stand only: it streams a
// deterministic byte sequence at a set rate, so a backup lasts as long as a
// test needs, whatever the host's disk. sard-agent links it only when built
// with the "e2e" tag (plugins/builtin_e2e.go); a release build never does.
//
// The data of seed s is the concatenation of blocks SHA-256(s || i),
// i = 0, 1, …, both big-endian uint64, cut to size: a test recomputes it to
// check a restored copy.
package e2eslow

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Filename is the name of the streamed file inside the snapshot.
const Filename = "e2e-slow.bin"

// Config is the plugin's configuration (schema.json).
type Config struct {
	Size  uint64 `json:"size"`  // bytes to stream
	Rate  uint64 `json:"rate"`  // bytes per second
	Chunk uint64 `json:"chunk"` // bytes per write and progress report
	Seed  uint64 `json:"seed"`
}

// Clock is the time the plugin paces the stream by.
type Clock interface {
	Now() time.Time
	// Sleep waits d, or returns ctx.Err() as soon as ctx is done.
	Sleep(ctx context.Context, d time.Duration) error
}

// RealClock is the host's time.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep implements Clock.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Plugin is the slow stream source.
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
	// Clock paces the stream; nil is RealClock.
	Clock Clock
}

var _ sdk.Plugin = Plugin{}

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "e2e-slow" }

// Version implements sdk.Plugin: the plugin ships with the agent.
func (p Plugin) Version() string { return p.AgentVersion }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

func parse(cfg sdk.Config) (Config, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// Prepare implements sdk.Plugin: the schema says all there is to check.
func (Plugin) Prepare(_ context.Context, _ sdk.Host, cfg sdk.Config) error {
	_, err := parse(cfg)
	return err
}

// Dump implements sdk.Plugin: the source is one streamed file.
func (Plugin) Dump(_ context.Context, _ sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	if _, err := parse(cfg); err != nil {
		return sdk.Dump{}, err
	}
	return sdk.Dump{Filename: Filename}, nil
}

// Stream implements sdk.Plugin: it writes Size bytes in Chunk-sized writes,
// reports progress after each and then sleeps until the moment Rate allows
// for the bytes written so far.
func (p Plugin) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, _ sdk.Dump, w io.Writer) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	s := &stream{c: c, clock: p.clock(), w: w, data: &blocks{seed: c.Seed}, buf: make([]byte, min(c.Chunk, c.Size))}
	s.start = s.clock.Now()
	for s.done < c.Size {
		if err := s.chunk(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func (p Plugin) clock() Clock {
	if p.Clock == nil {
		return RealClock{}
	}
	return p.Clock
}

// stream is one Stream call in progress.
type stream struct {
	c     Config
	clock Clock
	w     io.Writer
	data  io.Reader
	buf   []byte // one chunk: written in one Write, not a syscall per hash block
	start time.Time
	done  uint64
}

// chunk writes the next chunk, reports progress and keeps to the rate.
func (s *stream) chunk(ctx context.Context, h sdk.Host) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n := min(s.c.Chunk, s.c.Size-s.done)
	buf := s.buf[:n]
	_, _ = io.ReadFull(s.data, buf) // blocks never fails
	if _, err := s.w.Write(buf); err != nil {
		return err
	}
	s.done += n
	h.Progress(s.done, s.c.Size)
	return pace(ctx, s.clock, s.start, s.done, s.c.Rate)
}

// pace sleeps until start + done/rate seconds, if that is still ahead.
func pace(ctx context.Context, clock Clock, start time.Time, done, rate uint64) error {
	due := start.Add(time.Duration(float64(done) / float64(rate) * float64(time.Second)))
	if wait := due.Sub(clock.Now()); wait > 0 {
		return clock.Sleep(ctx, wait)
	}
	return nil
}

// blocks reads the endless data of seed: SHA-256(seed || i) for i = 0, 1, ….
type blocks struct {
	seed, next uint64
	buf        []byte
}

func (b *blocks) Read(p []byte) (int, error) {
	if len(b.buf) == 0 {
		var in [16]byte
		binary.BigEndian.PutUint64(in[:8], b.seed)
		binary.BigEndian.PutUint64(in[8:], b.next)
		sum := sha256.Sum256(in[:])
		b.buf, b.next = sum[:], b.next+1
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}
