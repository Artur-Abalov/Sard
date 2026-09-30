// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"
)

const pem = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\nBKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----\n"

func mustNew(t *testing.T, dst *bytes.Buffer, values ...string) *Writer {
	t.Helper()
	vs := make([][]byte, len(values))
	for i, v := range values {
		vs[i] = []byte(v)
	}
	w, err := New(dst, vs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}

// redactChunks writes chunks one by one, closes, and returns the output.
func redactChunks(t *testing.T, values []string, chunks ...string) string {
	t.Helper()
	var out bytes.Buffer
	w := mustNew(t, &out, values...)
	for _, c := range chunks {
		if n, err := w.Write([]byte(c)); err != nil || n != len(c) {
			t.Fatalf("Write(%q) = %d, %v", c, n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.String()
}

func TestRedactTable(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		in     string
		want   string
	}{
		{"single value", []string{"s3cret"}, "pw=s3cret;", "pw=[REDACTED];"},
		{"every occurrence", []string{"s3cret"}, "s3cret s3cret", "[REDACTED] [REDACTED]"},
		{"value at start and end", []string{"s3cret"}, "s3cret-x-s3cret", "[REDACTED]-x-[REDACTED]"},
		{"overlapping values merge", []string{"abcd", "cdef"}, "xabcdefx", "x[REDACTED]x"},
		{"later longer value extends leftwards", []string{"bc", "abcd"}, "-abcd-", "-[REDACTED]-"},
		{"value inside another", []string{"abcdef", "cd"}, "abcdef cd", "[REDACTED] [REDACTED]"},
		{"prefix value alone", []string{"abc", "abcdef"}, "abcx", "[REDACTED]x"},
		{"prefix value inside longer", []string{"abc", "abcdef"}, "abcdefx", "[REDACTED]x"},
		{"adjacent occurrences merge", []string{"ab"}, "abab", "[REDACTED]"},
		{"self-overlapping value", []string{"aa"}, "aaa b", "[REDACTED] b"},
		{"partial value is kept", []string{"s3cret"}, "s3cre", "s3cre"},
		{"multiline PEM", []string{pem}, "key:\n" + pem + "done\n", "key:\n[REDACTED]done\n"},
		{"url-encoded in DSN", []string{"p@ss w/rd"}, "postgres://u:p%40ss%20w%2Frd@db/x", "postgres://u:[REDACTED]@db/x"},
		{"query-encoded lowercase hex", []string{"a/b c"}, "?pw=a%2fb+c&x", "?pw=[REDACTED]&x"},
		{"json-escaped in restic output", []string{"pa\"ss\nword"}, `{"message":"bad pa\"ss\nword"}`, `{"message":"bad [REDACTED]"}`},
		{"json-escaped PEM", []string{pem}, `{"err":"` + strings.ReplaceAll(pem, "\n", `\n`) + `"}`, `{"err":"[REDACTED]"}`},
		{"short value still masked", []string{"x"}, "axb", "a[REDACTED]b"},
		{"no values", nil, "plain", "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactChunks(t, tc.values, tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValueSplitAtEveryByte(t *testing.T) {
	for _, value := range []string{"s3cret", pem, "p@ss w/rd"} {
		for _, in := range []string{"<" + value + ">", "<" + url.QueryEscape(value) + ">"} {
			want := redactChunks(t, []string{value}, in)
			if want != "<[REDACTED]>" {
				t.Fatalf("whole: %q", want)
			}
			for i := 1; i < len(in); i++ {
				if got := redactChunks(t, []string{value}, in[:i], in[i:]); got != want {
					t.Fatalf("split at %d: got %q", i, got)
				}
			}
		}
	}
}

func TestEveryByteAsOwnChunk(t *testing.T) {
	in := "a=" + pem + " b=abcdef c=abc"
	chunks := strings.Split(in, "")
	if got := redactChunks(t, []string{pem, "abc", "cdef"}, chunks...); got != "a=[REDACTED] b=[REDACTED] c=[REDACTED]" {
		t.Fatalf("got %q", got)
	}
}

// writeExpect writes in and checks everything dst has received so far.
func writeExpect(t *testing.T, w *Writer, out *bytes.Buffer, in, want string) {
	t.Helper()
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Fatalf("after writing %q: dst has %q, want %q", in, out.String(), want)
	}
}

func TestValueAtEndIsHeldUntilClose(t *testing.T) {
	var out bytes.Buffer
	w := mustNew(t, &out, "s3cret")
	writeExpect(t, w, &out, "pw=s3c", "pw=")
	writeExpect(t, w, &out, "ret", "pw=")
	if err := w.Flush(); err != nil || out.String() != "pw=" {
		t.Fatalf("Flush = %v; value at end leaked before Close: %q", err, out.String())
	}
	if err := w.Close(); err != nil || out.String() != "pw=[REDACTED]" {
		t.Fatalf("Close = %v; dst has %q", err, out.String())
	}
}

func TestPossiblePrefixReleasedOnClose(t *testing.T) {
	var out bytes.Buffer
	w := mustNew(t, &out, "s3cret")
	_, _ = w.Write([]byte("tail s3cr"))
	if out.String() != "tail " {
		t.Fatalf("before Close: %q", out.String())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "tail s3cr" {
		t.Fatalf("after Close: %q", out.String())
	}
}

func TestSafeBytesAreWrittenImmediately(t *testing.T) {
	var out bytes.Buffer
	w := mustNew(t, &out, "s3cret")
	_, _ = w.Write([]byte("hello "))
	if out.String() != "hello " {
		t.Fatalf("got %q", out.String())
	}
	_, _ = w.Write([]byte("s3cret!"))
	if out.String() != "hello [REDACTED]!" {
		t.Fatalf("got %q", out.String())
	}
}

func TestHeldTailBoundedByLongestValue(t *testing.T) {
	var out bytes.Buffer
	w := mustNew(t, &out, "aaaa", "ab")
	for range 100 {
		_, _ = w.Write([]byte("a"))
		if len(w.pend) > 4 {
			t.Fatalf("held %d bytes, longest value is 4", len(w.pend))
		}
	}
	_ = w.Close()
	if out.String() != "[REDACTED]" {
		t.Fatalf("got %q", out.String())
	}
}

func TestNewRejectsEmptyValueWithoutEchoingValues(t *testing.T) {
	_, err := New(&bytes.Buffer{}, [][]byte{[]byte("topsecret"), {}})
	if !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "topsecret") || !strings.Contains(err.Error(), "value 1") {
		t.Fatalf("error text: %q", err.Error())
	}
}

func TestShortValuesAreReportedByIndexAndLength(t *testing.T) {
	var got [][2]int
	values := [][]byte{[]byte("1234567"), []byte("12345678"), []byte("ab")}
	_, err := New(&bytes.Buffer{}, values, OnShortValue(func(index, length int) {
		got = append(got, [2]int{index, length})
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{0, 7}, {2, 2}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("short values reported: %v, want %v", got, want)
	}
}

func TestValuesAreCopied(t *testing.T) {
	v := []byte("s3cret")
	var out bytes.Buffer
	w, err := New(&out, [][]byte{v})
	if err != nil {
		t.Fatal(err)
	}
	copy(v, "xxxxxx")
	_, _ = w.Write([]byte("s3cret"))
	_ = w.Close()
	if out.String() != Marker {
		t.Fatalf("got %q", out.String())
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestDestinationErrorIsSticky(t *testing.T) {
	boom := errors.New("disk full")
	w, err := New(failWriter{boom}, [][]byte{[]byte("s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("hello")); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("more")); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("second Write = %d, %v", n, err)
	}
	assertStickyAfterWrite(t, w, boom)
}

func assertStickyAfterWrite(t *testing.T, w *Writer, boom error) {
	t.Helper()
	if err := w.Flush(); !errors.Is(err, boom) {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); !errors.Is(err, boom) {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseErrorFromDestination(t *testing.T) {
	boom := errors.New("pipe closed")
	w, err := New(failWriter{boom}, [][]byte{[]byte("s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("s3c")); err != nil {
		t.Fatalf("held-only Write must not touch dst: %v", err)
	}
	if err := w.Close(); !errors.Is(err, boom) {
		t.Fatalf("Close: %v", err)
	}
}

func TestWriteAfterClose(t *testing.T) {
	var out bytes.Buffer
	w := mustNew(t, &out, "s3cret")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("x")); !errors.Is(err, ErrClosed) || n != 0 {
		t.Fatalf("Write after Close = %d, %v", n, err)
	}
	if err := w.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

type flushRecorder struct {
	bytes.Buffer
	flushes int
	err     error
}

func (f *flushRecorder) Flush() error { f.flushes++; return f.err }

func TestFlushPropagatesToFlushableDestination(t *testing.T) {
	dst := &flushRecorder{}
	w, err := New(dst, [][]byte{[]byte("s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("ok s3c"))
	if err := w.Flush(); err != nil || dst.flushes != 1 {
		t.Fatalf("Flush = %v, flushes = %d", err, dst.flushes)
	}
	if dst.String() != "ok " {
		t.Fatalf("Flush released the held tail: %q", dst.String())
	}
	dst.err = errors.New("flush failed")
	if err := w.Flush(); !errors.Is(err, dst.err) {
		t.Fatalf("Flush error: %v", err)
	}
}
