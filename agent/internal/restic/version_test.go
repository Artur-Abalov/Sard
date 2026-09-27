// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func versionReply(stdout string) reply { return reply{inline: stdout} }

// Golden: the pinned restic.
func TestVersionOfThePinnedRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"version": {stdout: "version.txt"}})
	v, err := f.build().Version(context.Background())
	if err != nil || v != restic.Pinned || v.String() != "0.19.1" {
		t.Fatalf("Version = %v, %v", v, err)
	}
	// version needs no repository, key or backend credentials.
	got := f.exec.call("version")
	if !slices.Equal(got.Args, []string{"version"}) || slices.ContainsFunc(got.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "RESTIC_PASSWORD_FILE=") || strings.HasPrefix(kv, "RESTIC_REPOSITORY=")
	}) {
		t.Errorf("args = %q, env = %q", got.Args, got.Env)
	}
}

func TestVersionAcceptsTheMinimumAndNewer(t *testing.T) {
	for _, out := range []string{
		"restic 0.19.0 compiled with go1.26.4 on linux/amd64\n",
		"restic 0.20.0 compiled with go1.27.0 on linux/arm64\n",
		"restic 1.0.0 compiled with go1.27.0 on linux/amd64\n",
		"restic 0.19.2-dev (compiled manually) compiled with go1.26.8 on linux/amd64\n",
	} {
		f := newFixture(t, map[string]reply{"version": versionReply(out)})
		if _, err := f.build().Version(context.Background()); err != nil {
			t.Errorf("%q: %v", out, err)
		}
	}
}

func TestVersionRejectsOlderRestic(t *testing.T) {
	for _, out := range []string{
		"restic 0.18.1 compiled with go1.25.1 on linux/amd64\n",
		"restic 0.9.6 compiled with go1.13 on linux/amd64\n",
	} {
		f := newFixture(t, map[string]reply{"version": versionReply(out)})
		v, err := f.build().Version(context.Background())
		want := "unsupported restic version: restic " + strings.Fields(out)[1] + " is older than the minimum " + restic.Minimum.String()
		if !errors.Is(err, restic.ErrUnsupportedVersion) || err.Error() != want || v.String() != strings.Fields(out)[1] {
			t.Errorf("%q: v = %v, err = %v", out, v, err)
		}
	}
}

func TestVersionRejectsUnrecognizedOutput(t *testing.T) {
	for _, out := range []string{
		"",
		"rustic 0.19.1\n",
		"restic version unknown\n",
		"restic 0.19\n",
		"0.19.1 compiled with go1.26.4 on linux/amd64\n",
		"restic 0.19.x compiled\n",
		"restic 99999999999999999999.0.0\n",
	} {
		f := newFixture(t, map[string]reply{"version": versionReply(out)})
		if _, err := f.build().Version(context.Background()); !errors.Is(err, restic.ErrBadOutput) {
			t.Errorf("%q: err = %v", out, err)
		}
	}
}

func TestVersionFailure(t *testing.T) {
	f := newFixture(t, map[string]reply{"version": {code: 1}})
	var exitErr *restic.ExitError
	if _, err := f.build().Version(context.Background()); !errors.As(err, &exitErr) {
		t.Fatalf("err = %v", err)
	}
}

func TestVersionOrdering(t *testing.T) {
	ordered := []restic.Version{{Major: 0, Minor: 9, Patch: 9}, {Major: 0, Minor: 19, Patch: 0}, {Major: 0, Minor: 19, Patch: 1}, {Major: 1, Minor: 0, Patch: 0}}
	for i := range ordered {
		for j := range ordered {
			if got := ordered[i].Less(ordered[j]); got != (i < j) {
				t.Errorf("%v < %v = %v", ordered[i], ordered[j], got)
			}
		}
	}
}

// The pinned and minimum versions come from the one version file.
func TestPinnedVersionsComeFromTheVersionFile(t *testing.T) {
	data, err := os.ReadFile("restic-version")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "\nversion="+restic.Pinned.String()+"\n") ||
		!strings.Contains(text, "\nmin_version="+restic.Minimum.String()+"\n") || restic.Pinned.Less(restic.Minimum) {
		t.Fatalf("pinned %v, minimum %v", restic.Pinned, restic.Minimum)
	}
}

func TestVersionExecutorFailure(t *testing.T) {
	f := newFixture(t, map[string]reply{"version": {code: -1, err: fs.ErrNotExist}})
	if _, err := f.build().Version(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

// A broken version file stops the agent at start-up.
func TestBrokenVersionFilePanics(t *testing.T) {
	for _, file := range []string{"min_version=0.19.0\n", "version=0.19.1\n", "version=x\nmin_version=0.19.0\n"} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.HasPrefix(fmt.Sprint(r), "restic-version: ") {
					t.Errorf("%q: recovered %v", file, r)
				}
			}()
			restic.MustVersions(file)
		}()
	}
	if p, m := restic.MustVersions("# c\nversion=1.2.3\n min_version=1.0.0 \n"); p.String() != "1.2.3" || m.String() != "1.0.0" {
		t.Errorf("pinned %v, minimum %v", p, m)
	}
}
