// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import "testing"

// Scenario: Команда psql в любом месте строки вне кавычек — отказ.
func TestACommandOfPsqlAnywhereOutsideQuotesIsRefused(t *testing.T) {
	for _, line := range []string{`ALTER ROLE app_rw WITH LOGIN; \! touch /tmp/pwned`, "  \\! touch /tmp/pwned", "\t\\! touch /tmp/pwned", `SELECT 1 \gexec`} {
		if g := parseGlobals("CREATE ROLE app_rw;\n" + line + "\n"); g.meta == 0 {
			t.Errorf("%q: not seen as a command of psql", line)
		}
	}
}
