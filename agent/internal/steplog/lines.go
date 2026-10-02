// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package steplog turns the raw output of a tool a step runs (restic's
// stderr) into masked log lines (A7c). The stream is masked first and cut
// into lines after, so a secret split across writes or spanning lines is
// still found, and the line it was in reaches the log whole.
package steplog

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/redact"
)

// MaxLine is the longest line emitted; a longer one is cut into pieces.
// It matches the server's limit, which would truncate instead.
const MaxLine = 8192

// Lines masks what is written to it and emits it line by line. It is safe
// for concurrent use; emit is called with Lines' lock held, in order, and
// may block (back pressure slows the writer down).
type Lines struct {
	mu     sync.Mutex
	in     io.Writer // the masker, or split itself without values
	masker *redact.Writer
	split  splitter
	closed bool
}

// New returns Lines masking set's values; a nil set masks nothing.
func New(set *redact.Set, emit func(line string)) *Lines {
	l := &Lines{split: splitter{emit: emit}}
	l.in = &l.split
	if set != nil {
		l.masker = set.NewWriter(&l.split)
		l.in = l.masker
	}
	return l
}

// Write takes raw output. After Close it is discarded: the step is over.
// It never fails, so a tool writing to it is never disturbed.
func (l *Lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		_, _ = l.in.Write(p) // split never fails
	}
	return len(p), nil
}

// Close releases the masker's held-back tail and the last, unterminated
// line. A second Close finds nothing left to release.
func (l *Lines) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.masker != nil {
		_ = l.masker.Close() // split never fails
	}
	l.split.flush()
}

// splitter cuts masked output into lines without carriage returns.
type splitter struct {
	emit func(line string)
	buf  []byte
}

func (s *splitter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		switch {
		case i >= 0 && i <= MaxLine:
			s.send(s.buf[:i])
			s.buf = s.buf[i+1:]
		case len(s.buf) >= MaxLine:
			s.send(s.buf[:MaxLine])
			s.buf = s.buf[MaxLine:]
		default:
			return len(p), nil
		}
	}
}

func (s *splitter) flush() {
	if len(s.buf) > 0 {
		s.send(s.buf)
	}
	s.buf = nil
}

func (s *splitter) send(line []byte) {
	s.emit(strings.ToValidUTF8(string(bytes.Trim(line, "\r")), "�"))
}
