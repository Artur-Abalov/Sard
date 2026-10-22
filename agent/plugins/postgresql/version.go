// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"regexp"
	"strconv"
)

// minServerMajor is the oldest supported server (F1 ПГ19).
const minServerMajor = 14

// version is a PostgreSQL version; only the major number is compared (F1 ПГ7).
type version struct {
	text  string // 16.4, 18beta1
	major int
}

var clientVersion = regexp.MustCompile(`\(PostgreSQL\) ((\d+)\S*)`)

// parseClient reads the version from the output of `<tool> --version`.
func parseClient(tool, output string) (version, error) {
	v, ok := scanVersion(output)
	if !ok {
		return version{}, fmt.Errorf("cannot read the version of %s from its answer %q", tool, output)
	}
	return v, nil
}

// scanVersion finds the version in the output of a tool.
func scanVersion(output string) (version, bool) {
	m := clientVersion.FindStringSubmatch(output)
	if m == nil {
		return version{}, false
	}
	major, _ := strconv.Atoi(m[2]) // the pattern guarantees digits
	return version{text: m[1], major: major}, true
}

// parseTag reads the version of a tag of a snapshot ("16.4", "18beta1"); false
// for a value that does not start with a number.
func parseTag(text string) (version, bool) {
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return version{}, false
	}
	major, _ := strconv.Atoi(text[:end])
	return version{text: text, major: major}, true
}

// serverVersion reads server_version_num: 160004 is 16.4.
func serverVersion(num int) version {
	return version{text: fmt.Sprintf("%d.%d", num/10000, num%10000), major: num / 10000}
}
