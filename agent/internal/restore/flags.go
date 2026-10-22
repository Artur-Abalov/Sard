// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// CheckPaths: every --path is an absolute path inside the snapshot, taken
// literally: no pattern characters and no ".." component (Р61). The paths
// are returned in their clean form.
func CheckPaths(paths []string) ([]string, error) {
	clean := make([]string, len(paths))
	for i, p := range paths {
		if !path.IsAbs(p) || strings.ContainsAny(p, `*?[\`) || slices.Contains(strings.Split(p, "/"), "..") {
			return nil, fmt.Errorf("--path must be an absolute path taken literally, without * ? [ \\ and .., got %q", p)
		}
		clean[i] = path.Clean(p)
	}
	return clean, nil
}

// CheckTargetFlags: --target is given, and --allow-root-target goes with the
// root as the target only (Р59).
func CheckTargetFlags(target string, allowRoot bool) error {
	switch {
	case target == "":
		return errors.New("--target is required: the directory to restore into")
	case allowRoot && !IsRoot(target):
		return errors.New("--allow-root-target goes with --target / only")
	}
	return nil
}

// IsRoot: the target is the root of the file system, however it is spelled
// (the clean form of a relative path is never "/").
func IsRoot(target string) bool { return path.Clean(target) == "/" }

// OverwriteUnavailable is the usage error of --overwrite: replacing files in
// a target that is not empty is the slice A8c-2b (Р74, OQ-211). The flag is
// recognised, so that the operator sees a limit, not a typo.
const OverwriteUnavailable = "--overwrite is not available yet: restore into an empty or new directory"
