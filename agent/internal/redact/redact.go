// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package redact masks secret values in a byte stream that arrives in
// chunks of any size (A7a). Every occurrence of a value, in any of the
// encodings it can take in a tool's output (see variants), is replaced
// by Marker, even when it is split across chunks. Overlapping or adjacent
// occurrences become one Marker. The package does not know where the
// values come from, and never puts them into error texts.
//
// A Writer holds back the tail of the stream that may still be the start
// of a value — at most the length of the longest encoded value — and
// writes it out on Close. Everything else reaches the destination as soon
// as Write returns.
package redact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Marker replaces each masked range.
const Marker = "[REDACTED]"

// ShortValueLen: values shorter than this are masked all the same, but
// reported through OnShortValue — they are likely to mask innocent text.
const ShortValueLen = 8

var (
	// ErrEmptyValue: an empty value would match everywhere.
	ErrEmptyValue = errors.New("redact: empty value")
	// ErrClosed: Write or Flush after Close.
	ErrClosed = errors.New("redact: writer is closed")
)

type config struct {
	onShort func(index, length int)
}

// Option configures New.
type Option func(*config)

// OnShortValue registers f to be called, from New, with the index and
// length of every value shorter than ShortValueLen.
func OnShortValue(f func(index, length int)) Option {
	return func(c *config) { c.onShort = f }
}

// span is a range [start, end) of stream offsets to mask; marked is set
// once its Marker has been written.
type span struct {
	start, end int64
	marked     bool
}

// Writer masks values in everything written to it and passes the result
// to its destination. It is not safe for concurrent use; Close does not
// close the destination.
type Writer struct {
	dst   io.Writer
	ac    *automaton
	state int32
	pos   int64 // stream bytes consumed
	base  int64 // stream bytes before base are written out or dropped
	// Stream bytes [base, pos) are read from the held-back tail pend,
	// which ends at stream offset curAt, and then from cur, the chunk
	// being written (starting at curAt). Only the tail is copied.
	pend  []byte
	spare []byte // pend's previous buffer, reused for the next tail
	cur   []byte
	curAt int64
	spans []span // sorted, disjoint, non-adjacent; ends <= pos
	out   []byte
	err   error
	done  bool
}

// Set is a compiled list of values to mask. It is read-only once built,
// so one Set serves any number of Writers at once.
type Set struct {
	ac *automaton
}

// Compile builds the matcher for values (copied; the caller may clear its
// own copies afterwards). Without values it returns a nil Set, which masks
// nothing: callers skip the matcher entirely.
func Compile(values [][]byte, opts ...Option) (*Set, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}
	patterns, err := patternsOf(values, c.onShort)
	if len(patterns) == 0 { // also on an error
		return nil, err
	}
	return &Set{ac: newAutomaton(patterns)}, nil
}

// patternsOf returns the variants of every value, reporting short ones to onShort.
func patternsOf(values [][]byte, onShort func(index, length int)) ([][]byte, error) {
	var patterns [][]byte
	for i, v := range values {
		if len(v) == 0 {
			return nil, fmt.Errorf("redact: value %d: %w", i, ErrEmptyValue)
		}
		if len(v) < ShortValueLen && onShort != nil {
			onShort(i, len(v))
		}
		patterns = append(patterns, variants(v)...)
	}
	return patterns, nil
}

// NewWriter returns a Writer masking the set's values on the way to dst.
func (s *Set) NewWriter(dst io.Writer) *Writer {
	return &Writer{dst: dst, ac: s.ac}
}

// Mask returns text with every value masked; a nil Set returns it as is.
func (s *Set) Mask(text string) string {
	if s == nil {
		return text
	}
	var out bytes.Buffer
	w := s.NewWriter(&out)
	_, _ = w.Write([]byte(text)) // a bytes.Buffer does not fail
	_ = w.Close()
	return out.String()
}

// New returns a Writer masking values (copied; the caller may clear its
// own copies afterwards) on the way to dst.
func New(dst io.Writer, values [][]byte, opts ...Option) (*Writer, error) {
	s, err := Compile(values, opts...)
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = &Set{ac: newAutomaton(nil)}
	}
	return s.NewWriter(dst), nil
}

// Write masks p and writes out everything that can no longer be part of
// a value. It returns len(p) unless the destination fails; a destination
// error is returned by every later call.
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.usable(); err != nil {
		return 0, err
	}
	w.cur, w.curAt = p, w.pos
	for _, b := range p {
		w.state = w.ac.step(w.state, b)
		w.pos++
		if n := int64(w.ac.longest[w.state]); n > 0 {
			w.addSpan(w.pos-n, w.pos)
		}
	}
	// No value that is still to be matched can start before the current
	// state's depth: everything left of it is final.
	w.settle(w.pos - int64(w.ac.depth[w.state]))
	w.holdTail()
	if err := w.emit(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush flushes the destination if it has a Flush() error method. It
// keeps the held-back tail: releasing it could leak a value split right
// at the flush.
func (w *Writer) Flush() error {
	if err := w.usable(); err != nil {
		return err
	}
	if f, ok := w.dst.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			return fmt.Errorf("redact: flush: %w", err)
		}
	}
	return nil
}

// Close writes out the held-back tail, masked if it completes a value.
// Later Writes fail with ErrClosed; a second Close returns nil.
func (w *Writer) Close() error {
	if w.done {
		return nil
	}
	w.done = true
	if w.err != nil {
		return w.err
	}
	w.settle(w.pos)
	return w.emit()
}

func (w *Writer) usable() error {
	if w.done {
		return ErrClosed
	}
	return w.err
}

// addSpan records [start, end), merging it with the spans it overlaps or
// touches. end is the current position, so only trailing spans can merge.
// Only spans[0] is ever marked, and it is the last one popped.
func (w *Writer) addSpan(start, end int64) {
	marked := false
	for n := len(w.spans); n > 0 && w.spans[n-1].end >= start; n-- {
		last := w.spans[n-1]
		start = min(start, last.start)
		marked = last.marked
		w.spans = w.spans[:n-1]
	}
	w.spans = append(w.spans, span{start: start, end: end, marked: marked})
}

// settle moves every byte before offset final to the output: plain bytes
// as they are, each span as one Marker.
func (w *Writer) settle(final int64) {
	for len(w.spans) > 0 && w.spans[0].start < final {
		sp := &w.spans[0]
		w.keep(sp.start)
		if !sp.marked {
			w.out = append(w.out, Marker...)
			sp.marked = true
		}
		w.drop(min(sp.end, final))
		if sp.end >= final {
			return // a later match may still extend this span
		}
		w.spans = slices.Delete(w.spans, 0, 1)
	}
	w.keep(final)
}

// keep writes the pending bytes before offset to the output.
func (w *Writer) keep(offset int64) {
	if offset > w.base {
		w.out = w.appendStream(w.out, w.base, offset)
		w.base = offset
	}
}

// drop discards the pending bytes before offset.
func (w *Writer) drop(offset int64) {
	w.base = max(w.base, offset)
}

// holdTail copies the stream bytes [base, pos) out of the current chunk,
// which the Writer must not retain, into pend.
func (w *Writer) holdTail() {
	w.spare = w.appendStream(w.spare[:0], w.base, w.pos)
	w.pend, w.spare = w.spare, w.pend
	w.cur, w.curAt = nil, w.pos
}

// appendStream appends stream bytes [from, to) to dst; from >= base.
func (w *Writer) appendStream(dst []byte, from, to int64) []byte {
	if from < w.curAt {
		tail := w.pend[len(w.pend)-int(w.curAt-from):]
		dst = append(dst, tail[:min(to, w.curAt)-from]...)
		if to <= w.curAt {
			return dst
		}
		from = w.curAt
	}
	return append(dst, w.cur[from-w.curAt:to-w.curAt]...)
}

func (w *Writer) emit() error {
	if len(w.out) == 0 {
		return nil
	}
	_, err := w.dst.Write(w.out)
	w.out = w.out[:0]
	if err != nil {
		w.err = fmt.Errorf("redact: %w", err)
	}
	return w.err
}
