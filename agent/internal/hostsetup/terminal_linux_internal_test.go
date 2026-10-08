// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux

package hostsetup

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestReadLineStopsAtTheLineBreakAndConsumesNothingMore(t *testing.T) {
	r := strings.NewReader("ab\ncd")
	got, err := readLine(r)
	if err != nil || string(got) != "ab" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "cd" {
		t.Fatalf("rest %q", rest)
	}
}

func TestReadLineTakesALastLineWithoutABreak(t *testing.T) {
	for _, line := range []string{"a", "abc"} {
		if got, err := readLine(strings.NewReader(line)); err != nil || string(got) != line {
			t.Fatalf("got %q, err %v", got, err)
		}
	}
}

func TestReadLineOfNothingIsTheEndOfInput(t *testing.T) {
	if got, err := readLine(strings.NewReader("")); !errors.Is(err, io.EOF) || got != nil {
		t.Fatalf("got %q, err %v", got, err)
	}
}

type failAfter struct {
	data string
	err  error
}

func (f *failAfter) Read(p []byte) (int, error) {
	if f.data == "" {
		return 0, f.err
	}
	p[0], f.data = f.data[0], f.data[1:]
	return 1, nil
}

func TestReadLineKeepsNoPartialLineWhenTheReadFails(t *testing.T) {
	boom := errors.New("read broke")
	if got, err := readLine(&failAfter{data: "ab", err: boom}); !errors.Is(err, boom) || got != nil {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestTcsetOnAFileThatIsNotATerminalFails(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := tcset(f.Fd(), &syscall.Termios{}); !errors.Is(err, syscall.ENOTTY) {
		t.Fatalf("err = %v", err)
	}
}

type endless struct{}

func (endless) Read(p []byte) (int, error) { p[0] = 'a'; return 1, nil }

func TestReadLineStopsOneByteBeyondTheLargestSecret(t *testing.T) {
	got, err := readLine(endless{})
	if err != nil || len(got) != MaxSecretSize+1 {
		t.Fatalf("read %d bytes, err %v", len(got), err)
	}
}
