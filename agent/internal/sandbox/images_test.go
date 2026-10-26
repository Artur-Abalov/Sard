// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
)

// The list ships with every release: each line a name, the semantic version
// and its digest (owner's decision 8).
func TestTheListOfTheReleaseHasEveryMajorVersionOfPostgresql(t *testing.T) {
	list := sandbox.ReleaseImages()
	for _, major := range []string{"14", "15", "16", "17", "18"} {
		img, ok := sandbox.Pick(list, "postgres", major)
		if !ok || !strings.HasPrefix(img.Version, major+".") {
			t.Errorf("postgres %s: %+v", major, img)
		}
	}
}

func TestTheNewestVersionWinsByNumbersNotByText(t *testing.T) {
	list := []sandbox.Image{
		{Name: "postgres", Version: "17.9", Digest: "sha256:" + strings.Repeat("a", 64)},
		{Name: "postgres", Version: "17.11", Digest: "sha256:" + strings.Repeat("b", 64)},
		{Name: "postgres", Version: "9.6", Digest: "sha256:" + strings.Repeat("c", 64)},
	}
	if img, _ := sandbox.Pick(list, "postgres", "17"); img.Version != "17.11" {
		t.Errorf("major 17: %+v", img)
	}
	if img, _ := sandbox.Pick(list, "postgres", ""); img.Version != "17.11" {
		t.Errorf("newest: %+v", img)
	}
	if _, ok := sandbox.Pick(list, "postgres", "1"); ok {
		t.Error("17 is no major version 1")
	}
}

func TestAListLineThatIsNoImageIsAnError(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, line := range []string{
		"postgres 17.6", "postgres 17.6 " + digest + " extra", "post:gres 17.6 " + digest, "postgres seventeen " + digest,
		"postgres 17.-1 " + digest, "postgres 17.6 sha256:abc", "postgres 17.6 md5:" + strings.Repeat("a", 64),
		"postgres 17.6 sha256:" + strings.Repeat("A", 64),
	} {
		if _, err := sandbox.ParseImages("# a comment\n\n" + line + "\n"); err == nil || !strings.Contains(err.Error(), "line 3") {
			t.Errorf("%q: err = %v", line, err)
		}
	}
	list, err := sandbox.ParseImages("postgres 17.6 " + digest + " # the newest\n")
	if err != nil || len(list) != 1 || list[0].Version != "17.6" {
		t.Fatalf("list %+v, err %v", list, err)
	}
}
