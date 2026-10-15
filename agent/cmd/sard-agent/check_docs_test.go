// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	troubleshooting = "../../../docs/operator/09-troubleshooting.md"
	hostCheckSpec   = "../../../docs/specs/agent/host-check.feature"
)

// slug is the anchor GitHub gives to a heading: lower case, no punctuation
// but "-" and "_", spaces as "-".
func slug(title string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' {
			return '-'
		}
		if strings.ContainsRune("-_0123456789abcdefghijklmnopqrstuvwxyzабвгдеёжзийклмнопрстуфхцчшщъыьэюя", r) {
			return r
		}
		return -1
	}, strings.ToLower(title))
}

// anchorsOf are the anchors of the headings of a Markdown file outside code blocks.
func anchorsOf(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var anchors []string
	fenced := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if title, found := strings.CutPrefix(strings.TrimLeft(line, "#"), " "); found && !fenced && strings.HasPrefix(line, "#") {
			anchors = append(anchors, slug(title))
		}
	}
	return anchors
}

// A8d-4, Ч30: the guide has one section, with the anchor of the identifier
// without dots, for every check.
func TestTroubleshootingHasOneAnchorForEveryCheck(t *testing.T) {
	anchors := anchorsOf(t, troubleshooting)
	ids := append(slices.Clone(wantIDs), "repository.integrity")
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		anchor := strings.ReplaceAll(id, ".", "")
		switch n := strings.Count(strings.Join(anchors, "\n")+"\n", "\n"+anchor+"\n"); n {
		case 1:
		default:
			t.Errorf("anchor #%s of %s: %d sections, want 1", anchor, id, n)
		}
	}
}

// Every link of a hint in the specification opens an existing anchor.
func TestEveryAnchorOfAHintExists(t *testing.T) {
	anchors := anchorsOf(t, troubleshooting)
	spec, err := os.ReadFile(hostCheckSpec)
	if err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`09-troubleshooting\.md#([a-z0-9_-]+)`).FindAllStringSubmatch(string(spec), -1)
	if len(links) == 0 {
		t.Fatal("no hint links found in the specification")
	}
	for _, m := range links {
		if !slices.Contains(anchors, m[1]) {
			t.Errorf("hint link #%s has no section in %s", m[1], troubleshooting)
		}
	}
}
