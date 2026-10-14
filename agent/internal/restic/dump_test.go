// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// A8c-3 (Р62): restic dump gives the content of one file of a snapshot; the
// agent writes the file itself.

func TestDumpWritesTheBytesOfTheFileToTheWriterAndRunsResticWithoutLock(t *testing.T) {
	f := newFixture(t, map[string]reply{"dump": {raw: "PGDMP\r\n\x00tail"}})
	var out bytes.Buffer
	if err := f.build().Dump(context.Background(), fullID, "/app.dump", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "PGDMP\r\n\x00tail" {
		t.Errorf("bytes %q", out.String())
	}
	want := []string{"dump", "--no-lock", "--", fullID, "/app.dump"}
	if args := f.exec.call("dump").Args; !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestDumpNeedsASnapshotAndAPath(t *testing.T) {
	f := newFixture(t, map[string]reply{"dump": {}})
	for _, c := range [][2]string{{"", "/a"}, {fullID, ""}} {
		err := f.build().Dump(context.Background(), c[0], c[1], &bytes.Buffer{})
		if !errors.Is(err, restic.ErrInvalidRequest) {
			t.Errorf("%q: err = %v", c, err)
		}
	}
	if len(f.exec.calls) != 0 {
		t.Errorf("restic ran: %v", f.exec.calls)
	}
}

func TestDumpFailureCarriesTheResticMessage(t *testing.T) {
	f := newFixture(t, map[string]reply{"dump": {code: 1}})
	f.exec.fatal = "Fatal: read tcp: connection reset by peer"
	err := f.build().Dump(context.Background(), fullID, "/app.dump", &bytes.Buffer{})
	var exit *restic.ExitError
	if !errors.As(err, &exit) || !errors.Is(err, restic.ErrNetwork) {
		t.Fatalf("err = %v", err)
	}
}
