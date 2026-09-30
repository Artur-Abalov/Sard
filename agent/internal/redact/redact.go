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
	"errors"
	"fmt"
	"io"
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
	pos   int64  // stream bytes consumed
	base  int64  // stream offset of pend[0]
	pend  []byte // stream bytes [base, pos) not yet written or dropped
	spans []span // sorted, disjoint, non-adjacent; ends <= pos
	out   []byte
	err   error
	done  bool
}

// New returns a Writer masking values (copied; the caller may clear its
// own copies afterwards) on the way to dst.
func New(dst io.Writer, values [][]byte, opts ...Option) (*Writer, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}
	var patterns [][]byte
	for i, v := range values {
		if len(v) == 0 {
			return nil, fmt.Errorf("redact: value %d: %w", i, ErrEmptyValue)
		}
		if len(v) < ShortValueLen && c.onShort != nil {
			c.onShort(i, len(v))
		}
		patterns = append(patterns, variants(v)...)
	}
	return &Writer{dst: dst, ac: newAutomaton(patterns)}, nil
}

// Write masks p and writes out everything that can no longer be part of
// a value. It returns len(p) unless the destination fails; a destination
// error is returned by every later call.
func (w *Writer) Write(p []byte) (int, error) {
	if err := w.usable(); err != nil {
		return 0, err
	}
	w.pend = append(w.pend, p...)
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
		w.spans = w.spans[1:]
	}
	w.keep(final)
}

// keep writes the pending bytes before offset to the output.
func (w *Writer) keep(offset int64) {
	if offset > w.base {
		w.out = append(w.out, w.pend[:offset-w.base]...)
		w.drop(offset)
	}
}

// drop discards the pending bytes before offset.
func (w *Writer) drop(offset int64) {
	if offset > w.base {
		w.pend = w.pend[offset-w.base:]
		w.base = offset
	}
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
