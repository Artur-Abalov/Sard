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
	m := clientVersion.FindStringSubmatch(output)
	if m == nil {
		return version{}, fmt.Errorf("cannot read the version of %s from its answer %q", tool, output)
	}
	major, _ := strconv.Atoi(m[2]) // the pattern guarantees digits
	return version{text: m[1], major: major}, nil
}

// serverVersion reads server_version_num: 160004 is 16.4.
func serverVersion(num int) version {
	return version{text: fmt.Sprintf("%d.%d", num/10000, num%10000), major: num / 10000}
}
