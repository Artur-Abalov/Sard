// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"path/filepath"
	"strings"
)

// excluder tells the paths that an --exclude pattern of restic leaves out of
// the snapshot, so that the sample holds none of them: a sampled file that is
// not in the snapshot would be a false mismatch of a restore check. It follows
// restic's filter.Match: a pattern is split into components and matches a run
// of the components of the path, at its start when the pattern is absolute,
// anywhere otherwise; "**" matches any number of components, the others are
// filepath.Match patterns. A negated pattern ("!…") re-includes in restic; it
// is ignored here, so the sample may skip a file restic keeps, never the
// reverse.
type excluder [][]string

func newExcluder(patterns []string) excluder {
	var out excluder
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "!") {
			continue
		}
		out = append(out, components(filepath.Clean(p)))
	}
	return out
}

// components splits a clean path; an absolute one starts with "/".
func components(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if strings.HasPrefix(p, "/") {
		return append([]string{"/"}, parts...)
	}
	return parts
}

// excludes reports whether a pattern matches the absolute path p.
func (e excluder) excludes(p string) bool {
	path := components(filepath.Clean(p))
	for _, pattern := range e {
		if matchAnywhere(pattern, path) {
			return true
		}
	}
	return false
}

func matchAnywhere(pattern, path []string) bool {
	if pattern[0] == "/" {
		return matchPrefix(pattern[1:], path[1:])
	}
	for offset := 1; offset < len(path); offset++ {
		if matchPrefix(pattern, path[offset:]) {
			return true
		}
	}
	return false
}

// matchAnyDepth: "**" followed by pattern matches after any number of components.
func matchAnyDepth(pattern, path []string) bool {
	for skip := 0; skip <= len(path); skip++ {
		if matchPrefix(pattern, path[skip:]) {
			return true
		}
	}
	return false
}

// matchPrefix reports whether pattern matches the first components of path.
func matchPrefix(pattern, path []string) bool {
	if len(pattern) == 0 {
		return true
	}
	if pattern[0] == "**" {
		return matchAnyDepth(pattern[1:], path)
	}
	if len(path) == 0 {
		return false
	}
	ok, err := filepath.Match(pattern[0], path[0])
	return ok && err == nil && matchPrefix(pattern[1:], path[1:])
}
