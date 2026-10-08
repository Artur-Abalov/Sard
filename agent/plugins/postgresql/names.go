// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"strings"
)

// safeBytes are the bytes of a database name that stay as they are.
const safeBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"

// encodeName writes the bytes of a database name outside [A-Za-z0-9_-] as
// %XX (upper-case hex), so the name is a safe, reversible file name: it is
// never "." or "..", and has no "/" or comma (F1 ПГ12).
func encodeName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if strings.IndexByte(safeBytes, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
