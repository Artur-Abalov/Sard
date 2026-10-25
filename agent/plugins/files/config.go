// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Limits that keep restic's command line short (A6b Ф6). The schema counts
// characters, these count bytes.
const (
	maxPathBytes    = 4096
	maxPatternBytes = 1024
)

// Defaults of the sample (V2).
const (
	defaultSampleFiles        = 100
	defaultSampleMaxFileBytes = 64 << 20
)

// config is a valid document of the schema.
type config struct {
	Paths              []string `json:"paths"`
	Exclude            []string `json:"exclude"`
	OneFileSystem      bool     `json:"one_file_system"`
	ControlPaths       []string `json:"control_paths"`
	SampleFiles        *int     `json:"sample_files"`
	SampleMaxFileBytes *int64   `json:"sample_max_file_bytes"`
}

func (c config) sampleFiles() int {
	if c.SampleFiles == nil {
		return defaultSampleFiles
	}
	return *c.SampleFiles
}

func (c config) sampleMaxFileBytes() int64 {
	if c.SampleMaxFileBytes == nil {
		return defaultSampleMaxFileBytes
	}
	return *c.SampleMaxFileBytes
}

// parse reads the config and reports what the schema cannot express: a
// string longer than the limit in bytes, a path that repeats or lies inside
// another after normalisation (A6b Ф7), a control path inside none of the
// paths (V2).
func parse(cfg sdk.Config) (config, error) {
	var c config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return c, &sdk.ConfigError{Violations: []sdk.Violation{{Message: err.Error()}}}
	}
	v := tooLong("/paths", c.Paths, maxPathBytes)
	v = append(v, tooLong("/exclude", c.Exclude, maxPatternBytes)...)
	v = append(v, overlapping(c.Paths)...)
	v = append(v, tooLong("/control_paths", c.ControlPaths, maxPathBytes)...)
	v = append(v, outside(c.ControlPaths, cleaned(c.Paths))...)
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

// outside reports the control paths that lie inside none of the paths.
func outside(control, paths []string) []sdk.Violation {
	var out []sdk.Violation
	for i, p := range cleaned(control) {
		if !slices.ContainsFunc(paths, func(root string) bool { return p == root || within(root, p) }) {
			out = append(out, sdk.Violation{Path: fmt.Sprintf("/control_paths/%d", i), Message: fmt.Sprintf("%q lies inside none of the paths", control[i])})
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
