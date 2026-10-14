// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rules Р59 and Р61 (A8c-2): what the flags of repo restore may say.

func TestPathsAreLiteralAbsoluteAndCleaned(t *testing.T) {
	got, err := restore.CheckPaths([]string{"/srv/data/", "/srv//data/sub", "/"})
	if err != nil || !slices.Equal(got, []string{"/srv/data", "/srv/data/sub", "/"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestAPathThatIsNotLiteralIsRefusedByName(t *testing.T) {
	for _, p := range []string{"srv/data", "/srv/*", "/srv/da?a", "/srv/[d]ata", `/srv/da\ta`, "/srv/data/../etc", ""} {
		if _, err := restore.CheckPaths([]string{"/ok", p}); err == nil || !strings.Contains(err.Error(), "--path") {
			t.Errorf("path %q: err = %v", p, err)
		}
	}
}

func TestADotDotInANameIsNotAParentComponent(t *testing.T) {
	if _, err := restore.CheckPaths([]string{"/srv/a..b"}); err != nil {
		t.Fatal(err)
	}
}

func TestTheTargetIsRequired(t *testing.T) {
	if err := restore.CheckTargetFlags("", false); err == nil || !strings.Contains(err.Error(), "--target") {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowRootTargetNeedsTheRootAsTheTarget(t *testing.T) {
	for _, target := range []string{"/srv/r", "relative"} {
		if err := restore.CheckTargetFlags(target, true); err == nil || !strings.Contains(err.Error(), "--allow-root-target") {
			t.Errorf("target %q: err = %v", target, err)
		}
	}
	for _, c := range []struct {
		target string
		allow  bool
	}{{"/", true}, {"//", true}, {"/.", true}, {"/srv/r", false}} {
		if err := restore.CheckTargetFlags(c.target, c.allow); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
}
