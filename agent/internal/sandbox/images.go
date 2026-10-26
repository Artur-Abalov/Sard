// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox

import (
	_ "embed"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// images.txt is the list of sandbox images this release of the agent knows:
// "<name> <semantic version> <digest>" per line. Each release refreshes it;
// the verify section of the agent's config can replace it.
//
//go:embed images.txt
var releaseImages string

// ReleaseImages is the list of this release.
func ReleaseImages() []Image {
	list, err := ParseImages(releaseImages)
	if err != nil {
		panic(err) // the list ships with the binary; a test reads it
	}
	return list
}

// ParseImages reads a list of images; "#" starts a comment.
func ParseImages(text string) ([]Image, error) {
	var list []Image
	for n, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		img := Image{}
		if len(fields) == 3 {
			img = Image{Name: fields[0], Version: fields[1], Digest: fields[2]}
		}
		if err := img.Valid(); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		list = append(list, img)
	}
	return list, nil
}

// Valid: a name, a semantic version and a sha256 digest.
func (i Image) Valid() error {
	hex, ok := strings.CutPrefix(i.Digest, "sha256:")
	switch {
	case i.Name == "" || strings.ContainsAny(i.Name, "@: "):
		return fmt.Errorf("image %q: want a name", i.Name)
	case version(i.Version) == nil:
		return fmt.Errorf("image %s: version %q: want numbers such as 17.6", i.Name, i.Version)
	case !ok || len(hex) != 64 || strings.Trim(hex, "0123456789abcdef") != "":
		return fmt.Errorf("image %s %s: digest %q: want sha256: and 64 hexadecimal characters", i.Name, i.Version, i.Digest)
	}
	return nil
}

// version is the numbers of a semantic version, nil when it is none.
func version(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// Pick is the newest image of list named name whose major version is major;
// any major version when major is empty.
func Pick(list []Image, name, major string) (Image, bool) {
	var fit []Image
	for _, img := range list {
		if img.Name == name && (major == "" || strings.Split(img.Version, ".")[0] == major) {
			fit = append(fit, img)
		}
	}
	if len(fit) == 0 {
		return Image{}, false
	}
	return slices.MaxFunc(fit, func(a, b Image) int { return slices.Compare(version(a.Version), version(b.Version)) }), true
}
