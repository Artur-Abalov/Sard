// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustCompile(t *testing.T, values ...string) *Set {
	t.Helper()
	vs := make([][]byte, len(values))
	for i, v := range values {
		vs[i] = []byte(v)
	}
	s, err := Compile(vs)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return s
}

func TestCompileWithoutValuesIsNil(t *testing.T) {
	s, err := Compile(nil)
	if s != nil || err != nil {
		t.Fatalf("Compile(nil) = %v, %v; want nil, nil", s, err)
	}
}

func TestNilSetMasksNothing(t *testing.T) {
	var s *Set
	if got := s.Mask("pw=s3cret"); got != "pw=s3cret" {
		t.Fatalf("Mask = %q", got)
	}
}

func TestCompileRejectsEmptyValueWithoutEchoingValues(t *testing.T) {
	_, err := Compile([][]byte{[]byte("topsecret"), {}})
	if !errors.Is(err, ErrEmptyValue) || strings.Contains(err.Error(), "topsecret") || !strings.Contains(err.Error(), "value 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestMaskMasksEveryVariantAndLeavesOtherTextAlone(t *testing.T) {
	s := mustCompile(t, "p@ss word")
	cases := map[string]string{
		"plain p@ss word end":         "plain [REDACTED] end",
		`{"m":"p@ss word"}`:           `{"m":"[REDACTED]"}`,
		"s3://u:p%40ss%20word@host/x": "s3://u:[REDACTED]@host/x",
		"nothing to hide":             "nothing to hide",
		"":                            "",
	}
	for in, want := range cases {
		if got := s.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskHandlesAValueAtTheVeryEnd(t *testing.T) {
	if got := mustCompile(t, "s3cret").Mask("pw=s3cret"); got != "pw=[REDACTED]" {
		t.Fatalf("Mask = %q", got)
	}
}

// One Set serves many writers at once: the automaton is read-only.
func TestWritersOfOneSetAreIndependent(t *testing.T) {
	s := mustCompile(t, "s3cret")
	var wg sync.WaitGroup
	outs := make([]bytes.Buffer, 8)
	for i := range outs {
		wg.Go(func() {
			w := s.NewWriter(&outs[i])
			for _, c := range []string{"a s3", "cr", fmt.Sprintf("et %d\n", i)} {
				_, _ = w.Write([]byte(c))
			}
			_ = w.Close()
		})
	}
	wg.Wait()
	for i := range outs {
		if want := fmt.Sprintf("a [REDACTED] %d\n", i); outs[i].String() != want {
			t.Errorf("writer %d: %q, want %q", i, outs[i].String(), want)
		}
	}
}
