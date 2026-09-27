// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// versionFile is the single source of the restic release shipped with
// the agent (docs/adr/0017-restic-shipped-with-agent.md).
//
//go:embed restic-version
var versionFile string

// Pinned is the restic release shipped with and tested against the agent;
// Minimum is the oldest release the agent accepts.
var Pinned, Minimum = mustVersions(versionFile)

// ErrUnsupportedVersion is returned for a restic older than Minimum.
var ErrUnsupportedVersion = errors.New("unsupported restic version")

// Version is a restic release number.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is an older release than w.
func (v Version) Less(w Version) bool {
	return cmp.Or(cmp.Compare(v.Major, w.Major), cmp.Compare(v.Minor, w.Minor), cmp.Compare(v.Patch, w.Patch)) < 0
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// parseVersion reads "X.Y.Z" at the start of s; a suffix such as "-dev" is ignored.
func parseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%w: not a restic version", ErrBadOutput)
	}
	var v Version
	if _, err := fmt.Sscanf(m[0], "%d.%d.%d", &v.Major, &v.Minor, &v.Patch); err != nil {
		return Version{}, fmt.Errorf("%w: %w", ErrBadOutput, err)
	}
	return v, nil
}

func mustVersions(file string) (pinned, minimum Version) {
	values := map[string]string{}
	for line := range strings.Lines(file) {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			values[key] = value
		}
	}
	pinned, err := parseVersion(values["version"])
	if err != nil {
		panic("restic-version: version: " + err.Error())
	}
	minimum, err = parseVersion(values["min_version"])
	if err != nil {
		panic("restic-version: min_version: " + err.Error())
	}
	return pinned, minimum
}

// Version runs `restic version` and fails with ErrUnsupportedVersion when
// the binary is older than Minimum. It needs no repository or key.
func (c *CLI) Version(ctx context.Context) (Version, error) {
	var out bytes.Buffer
	if _, err := c.run(ctx, c.baseEnv(), []string{"version"}, collect(&out)); err != nil {
		return Version{}, err
	}
	rest, ok := strings.CutPrefix(out.String(), "restic ")
	if !ok {
		return Version{}, fmt.Errorf("restic version: %w: not a restic version", ErrBadOutput)
	}
	v, err := parseVersion(rest)
	if err != nil {
		return Version{}, fmt.Errorf("restic version: %w", err)
	}
	if v.Less(Minimum) {
		return v, fmt.Errorf("%w: restic %s is older than the minimum %s", ErrUnsupportedVersion, v, Minimum)
	}
	return v, nil
}
