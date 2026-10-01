// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Limits that keep restic's command line short (A6b Ф6). The schema counts
// characters, these count bytes.
const (
	maxPathBytes    = 4096
	maxPatternBytes = 1024
)

// config is a valid document of the schema.
type config struct {
	Paths         []string `json:"paths"`
	Exclude       []string `json:"exclude"`
	OneFileSystem bool     `json:"one_file_system"`
}

// parse reads the config and reports what the schema cannot express: a
// string longer than the limit in bytes, a path that repeats or lies inside
// another after normalisation (A6b Ф7).
func parse(cfg sdk.Config) (config, error) {
	var c config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return c, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	v := tooLong("/paths", c.Paths, maxPathBytes)
	v = append(v, tooLong("/exclude", c.Exclude, maxPatternBytes)...)
	v = append(v, overlapping(c.Paths)...)
	if len(v) > 0 {
		return c, &sdk.ConfigError{Violations: v}
	}
	return c, nil
}

func tooLong(field string, values []string, limit int) []sdk.Violation {
	var out []sdk.Violation
	for i, s := range values {
		if len(s) > limit {
			out = append(out, sdk.Violation{Path: fmt.Sprintf("%s/%d", field, i), Message: fmt.Sprintf("longer than %d bytes", limit)})
		}
	}
	return out
}

// overlapping reports the pairs of paths that name the same place or one
// inside the other; the message names both as written.
func overlapping(paths []string) []sdk.Violation {
	var out []sdk.Violation
	norm := cleaned(paths)
	for i := range norm {
		for j := i + 1; j < len(norm); j++ {
			if norm[i] == norm[j] || within(norm[i], norm[j]) || within(norm[j], norm[i]) {
				out = append(out, sdk.Violation{Path: "/paths", Message: fmt.Sprintf("%q and %q are the same path or one lies inside the other", paths[i], paths[j])})
			}
		}
	}
	return out
}

func cleaned(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Clean(p)
	}
	return out
}

// within reports whether child lies below parent; both are clean.
func within(parent, child string) bool {
	return strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
}
