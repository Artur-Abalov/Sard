// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import "testing"

// The cases follow restic's filter.Match (internal/filter/filter_test.go).
func TestExcludePatternsMatchAsResticDoes(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"*.log", "/var/www/a.log", true},
		{"*.log", "/var/www/a.log/inner", true},
		{"*.log", "/var/www/a.txt", false},
		{"cache", "/var/www/cache/x/y", true},
		{"cache", "/var/www/cached", false},
		{"www/cache", "/var/www/cache/x", true},
		{"www/cache", "/www/cachex", false},
		{"/var/www/cache", "/var/www/cache/x", true},
		{"/var/www/cache", "/srv/var/www/cache", false},
		{"/var", "/var", true},
		{"/var/*/cache", "/var/www/cache", true},
		{"/var/**/cache", "/var/a/b/cache/f", true},
		{"/var/**/cache", "/var/cache", true},
		{"/var/**", "/var", true},
		{"**/tmp", "/a/tmp", true},
		{"/srv/**/*.tmp", "/srv/a.txt", false},
		{"[", "/a/[", false}, // a bad pattern matches nothing
		{"/a/./b/../c", "/a/c/d", true},
	} {
		if got := newExcluder([]string{tc.pattern}).excludes(tc.path); got != tc.want {
			t.Errorf("%q excludes %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestNegatedAndEmptyPatternsExcludeNothing(t *testing.T) {
	e := newExcluder([]string{"!keep", ""})
	if len(e) != 0 || e.excludes("/keep") || e.excludes("/a") {
		t.Errorf("excluder = %q", e)
	}
	if !newExcluder([]string{"!keep", "*.log"}).excludes("/x/a.log") {
		t.Error("a negated pattern hides the others")
	}
}
