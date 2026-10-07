// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// scriptedTerminal answers each prompt with the next value of its script
// and records the prompts.
type scriptedTerminal struct {
	values  []string
	err     error
	prompts []string
}

func (s *scriptedTerminal) ReadSecret(prompt string) ([]byte, error) {
	s.prompts = append(s.prompts, prompt)
	if s.err != nil {
		return nil, s.err
	}
	v := s.values[0]
	s.values = s.values[1:]
	return []byte(v), nil
}

// neverClosed is a pipe nobody writes to: reading it would hang forever.
type neverClosed struct{ t *testing.T }

func (n neverClosed) Read([]byte) (int, error) {
	n.t.Error("the command read its input although it should not have")
	return 0, io.EOF
}

var flagNames = hostsetup.SourceFlags{Stdin: "--stdin", File: "--from-file"}

func source(stdin io.Reader, term hostsetup.Terminal) hostsetup.Source {
	return hostsetup.Source{Stdin: stdin, Terminal: term, Open: func(p string) (io.ReadCloser, error) { return os.Open(p) }}
}

func TestStdinIsStoredByteForByte(t *testing.T) {
	in := "SECRET-MARKER\n\x00 tail"
	got, f := source(strings.NewReader(in), nil).Read(hostsetup.SourceOptions{Stdin: true, Flags: flagNames})
	if f != nil || string(got) != in {
		t.Fatalf("got %q, refusal %v", got, f)
	}
}

func TestFileIsStoredByteForByteAndNotChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("SECRET-MARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, f := source(nil, nil).Read(hostsetup.SourceOptions{File: path, Flags: flagNames})
	if f != nil || string(got) != "SECRET-MARKER\n" {
		t.Fatalf("got %q, refusal %v", got, f)
	}
	if data, _ := os.ReadFile(path); string(data) != "SECRET-MARKER\n" {
		t.Fatal("the source file changed")
	}
}

func TestMissingFileIsRefusedWithItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	_, f := source(nil, nil).Read(hostsetup.SourceOptions{File: path, Flags: flagNames})
	if f == nil || f.Class != repoinit.ClassUsage || !strings.Contains(f.Detail, path) {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTerminalAsksTwiceWithoutEchoAndKeepsTheLineAsTyped(t *testing.T) {
	term := &scriptedTerminal{values: []string{"SECRET-MARKER", "SECRET-MARKER"}}
	got, f := source(neverClosed{t}, term).Read(hostsetup.SourceOptions{Flags: flagNames, Subject: "secret db"})
	if f != nil || string(got) != "SECRET-MARKER" {
		t.Fatalf("got %q, refusal %v", got, f)
	}
	if len(term.prompts) != 2 || !strings.Contains(term.prompts[0], "secret db") || term.prompts[0] == term.prompts[1] {
		t.Fatalf("prompts = %q", term.prompts)
	}
}

func TestTerminalValuesThatDifferAreRefused(t *testing.T) {
	term := &scriptedTerminal{values: []string{"SECRET-MARKER-1", "SECRET-MARKER-2"}}
	_, f := source(nil, term).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretMismatch || strings.Contains(f.Error(), "MARKER") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTerminalThatFailsIsRefusedWithoutAValue(t *testing.T) {
	term := &scriptedTerminal{err: errors.New("terminal gone")}
	_, f := source(nil, term).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || f.Class != repoinit.ClassUsage || !strings.Contains(f.Detail, "terminal gone") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTerminalFailingOnTheSecondAskIsRefused(t *testing.T) {
	term := &failSecond{}
	_, f := source(nil, term).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || !strings.Contains(f.Detail, "second") {
		t.Fatalf("refusal = %+v", f)
	}
}

type failSecond struct{ n int }

func (s *failSecond) ReadSecret(string) ([]byte, error) {
	s.n++
	if s.n == 2 {
		return nil, errors.New("second ask failed")
	}
	return []byte("x"), nil
}

func TestWithoutFlagAndWithoutTerminalTheInputIsNeverRead(t *testing.T) {
	_, f := source(neverClosed{t}, nil).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretSourceMissing {
		t.Fatalf("refusal = %+v", f)
	}
	for _, want := range []string{"--stdin", "--from-file"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q lacks %s", f.Detail, want)
		}
	}
}

func TestMissingSourceNamesThePasswordFlagsOfRepoAdd(t *testing.T) {
	_, f := source(nil, nil).Read(hostsetup.SourceOptions{Flags: hostsetup.SourceFlags{Stdin: "--password-stdin", File: "--password-from-file"}})
	if f == nil || !strings.Contains(f.Detail, "--password-stdin") || !strings.Contains(f.Detail, "--password-from-file") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTwoSourcesAreRefusedBeforeAnythingIsRead(t *testing.T) {
	f := hostsetup.CheckSources(hostsetup.SourceOptions{Stdin: true, File: "F", Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretSourceConflict {
		t.Fatalf("refusal = %+v", f)
	}
	if hostsetup.CheckSources(hostsetup.SourceOptions{Stdin: true, Flags: flagNames}) != nil || hostsetup.CheckSources(hostsetup.SourceOptions{File: "F", Flags: flagNames}) != nil || hostsetup.CheckSources(hostsetup.SourceOptions{Flags: flagNames}) != nil {
		t.Fatal("one source or none was refused")
	}
}

func TestEmptyValueIsRefused(t *testing.T) {
	_, f := source(strings.NewReader(""), nil).Read(hostsetup.SourceOptions{Stdin: true, Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretEmpty {
		t.Fatalf("refusal = %+v", f)
	}
	term := &scriptedTerminal{values: []string{"", ""}}
	_, f = source(nil, term).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretEmpty {
		t.Fatalf("terminal: refusal = %+v", f)
	}
}

func TestLargestValueIsAcceptedAndOneByteMoreIsRefused(t *testing.T) {
	got, f := source(bytes.NewReader(bytes.Repeat([]byte("a"), 65536)), nil).Read(hostsetup.SourceOptions{Stdin: true, Flags: flagNames})
	if f != nil || len(got) != 65536 {
		t.Fatalf("65536 bytes: len %d, refusal %v", len(got), f)
	}
	_, f = source(bytes.NewReader(bytes.Repeat([]byte("a"), 65537)), nil).Read(hostsetup.SourceOptions{Stdin: true, Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretTooLarge || !strings.Contains(f.Detail, "65536") {
		t.Fatalf("65537 bytes: refusal = %+v", f)
	}
}

func TestFileOfTheLimitAndOverIsJudgedLikeStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), 65537), 0o600); err != nil {
		t.Fatal(err)
	}
	_, f := source(nil, nil).Read(hostsetup.SourceOptions{File: path, Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretTooLarge {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTooLargeTerminalValueIsRefused(t *testing.T) {
	big := strings.Repeat("a", 65537)
	_, f := source(nil, &scriptedTerminal{values: []string{big, big}}).Read(hostsetup.SourceOptions{Flags: flagNames})
	if f == nil || f.Reason != repoinit.SecretTooLarge {
		t.Fatalf("refusal = %+v", f)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read broke") }

func TestStdinThatFailsIsRefusedWithoutItsContent(t *testing.T) {
	_, f := source(failingReader{}, nil).Read(hostsetup.SourceOptions{Stdin: true, Flags: flagNames})
	if f == nil || f.Class != repoinit.ClassUsage || !strings.Contains(f.Detail, "read broke") {
		t.Fatalf("refusal = %+v", f)
	}
}
