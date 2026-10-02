// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package steplog_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/redact"
	"github.com/Artur-Abalov/sard/agent/internal/steplog"
)

const secret = "hunter2-very-secret"

type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) emit(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, line)
}

func (l *lines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

func compile(t *testing.T, values ...string) *redact.Set {
	t.Helper()
	vs := make([][]byte, len(values))
	for i, v := range values {
		vs[i] = []byte(v)
	}
	s, err := redact.Compile(vs)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func write(t *testing.T, w *steplog.Lines, chunks ...string) {
	t.Helper()
	for _, c := range chunks {
		if n, err := w.Write([]byte(c)); n != len(c) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", c, n, err)
		}
	}
}

func want(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestLinesWithoutSecretsPassUnchangedAndInOrder(t *testing.T) {
	var l lines
	w := steplog.New(compile(t, secret), l.emit)
	write(t, w, "first\nsec", "ond\n", "\n", "third")
	want(t, l.all(), "first", "second", "")
	w.Close()
	want(t, l.all(), "first", "second", "", "third")
}

func TestASecretSplitAcrossWritesIsMaskedAndItsLineIsWhole(t *testing.T) {
	for cut := 1; cut < len(secret); cut++ {
		var l lines
		w := steplog.New(compile(t, secret), l.emit)
		in := "Fatal: pw " + secret + " rejected\nnext\n"
		at := len("Fatal: pw ") + cut
		write(t, w, in[:at], in[at:])
		w.Close()
		want(t, l.all(), "Fatal: pw [REDACTED] rejected", "next")
	}
}

func TestEveryByteAsItsOwnWrite(t *testing.T) {
	var l lines
	w := steplog.New(compile(t, secret), l.emit)
	in := `{"message_type":"error","error":{"message":"` + secret + `"}}` + "\n"
	for i := range len(in) {
		write(t, w, in[i:i+1])
	}
	w.Close()
	want(t, l.all(), `{"message_type":"error","error":{"message":"[REDACTED]"}}`)
}

// A multi-line value (a PEM key) spans lines: masking comes before the cut.
func TestAMultiLineSecretBecomesOneMarker(t *testing.T) {
	pem := "-----BEGIN KEY-----\nAAAA\nBBBB\n-----END KEY-----"
	var l lines
	w := steplog.New(compile(t, pem), l.emit)
	write(t, w, "key:\n"+pem[:23], pem[23:]+"\ndone\n")
	w.Close()
	want(t, l.all(), "key:", "[REDACTED]", "done")
}

func TestASecretAtTheVeryEndIsReleasedMaskedOnClose(t *testing.T) {
	var l lines
	w := steplog.New(compile(t, secret), l.emit)
	write(t, w, "tail "+secret)
	want(t, l.all())
	w.Close()
	want(t, l.all(), "tail [REDACTED]")
}

func TestCarriageReturnsAroundLinesAreDropped(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	write(t, w, "\rprogress\r\nnext\r\n")
	w.Close()
	want(t, l.all(), "progress", "next")
}

func TestWithoutValuesLinesPassStraightThrough(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	write(t, w, "a\nb")
	want(t, l.all(), "a")
	w.Close()
	want(t, l.all(), "a", "b")
}

func TestALongLineIsCutIntoPiecesOfMaxLine(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	long := strings.Repeat("x", 2*steplog.MaxLine+5)
	write(t, w, long[:steplog.MaxLine-1], long[steplog.MaxLine-1:]+"\n")
	w.Close()
	want(t, l.all(), long[:steplog.MaxLine], long[steplog.MaxLine:2*steplog.MaxLine], "xxxxx")
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	write(t, w, "file \xff\xfe.txt\n")
	w.Close()
	want(t, l.all(), "file �.txt")
}

func TestWritesAfterCloseAreDiscarded(t *testing.T) {
	var l lines
	w := steplog.New(compile(t, secret), l.emit)
	w.Close()
	write(t, w, "late "+secret+"\n")
	w.Close()
	want(t, l.all())
}

func TestCloseWithNothingWrittenEmitsNothing(t *testing.T) {
	var l lines
	steplog.New(compile(t, secret), l.emit).Close()
	steplog.New(nil, l.emit).Close()
	want(t, l.all())
}

// Writes and Close may come from different goroutines (restic's output
// copier, the executor); every line arrives whole, masked, in order.
func TestConcurrentCloseNeverLeaksOrTearsALine(t *testing.T) {
	var l lines
	w := steplog.New(compile(t, secret), l.emit)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			_, _ = fmt.Fprintf(w, "line %d %s\n", i, secret)
		}
	})
	wg.Go(w.Close)
	wg.Wait()
	for i, line := range l.all() {
		if line != fmt.Sprintf("line %d [REDACTED]", i) {
			t.Fatalf("line %d = %q", i, line)
		}
	}
}

// The server keeps at most 8 KiB of a line and would truncate the rest.
func TestMaxLineIsTheServersLimit(t *testing.T) {
	if steplog.MaxLine != 8192 {
		t.Fatalf("MaxLine = %d", steplog.MaxLine)
	}
}

func TestALineOfExactlyMaxLineIsOneLine(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	full := strings.Repeat("y", steplog.MaxLine)
	write(t, w, full+"\nnext\n")
	w.Close()
	want(t, l.all(), full, "next")
}

func TestMaxLineBytesWithoutABreakAreSentAtOnce(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	full := strings.Repeat("y", steplog.MaxLine)
	write(t, w, full)
	want(t, l.all(), full)
}

func TestWritesAfterCloseAreDiscardedWithoutValuesToo(t *testing.T) {
	var l lines
	w := steplog.New(nil, l.emit)
	write(t, w, "a")
	w.Close()
	write(t, w, "late\n")
	w.Close()
	want(t, l.all(), "a")
}
