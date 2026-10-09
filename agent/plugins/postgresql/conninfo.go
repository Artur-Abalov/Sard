// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"strconv"
	"strings"
)

// applicationName marks the sessions of the agent on the server.
const applicationName = "sard-agent"

// conninfo is the libpq connection string of the config (F1 ПГ4): every
// value in single quotes, so that a database named "x host=evil" is a name.
// The password is not in it.
func (c config) conninfo() string {
	pairs := [][2]string{
		{"host", c.Host},
		{"port", strconv.Itoa(c.port())},
		{"dbname", c.Database},
		{"user", c.User},
		{"sslmode", c.sslmode()},
	}
	if c.TLSRootCert != "" {
		pairs = append(pairs, [2]string{"sslrootcert", c.TLSRootCert})
	}
	pairs = append(pairs,
		[2]string{"connect_timeout", "30"},
		[2]string{"keepalives_idle", "60"},
		[2]string{"keepalives_interval", "10"},
		[2]string{"keepalives_count", "6"},
		[2]string{"application_name", applicationName},
	)
	parts := make([]string, len(pairs))
	for i, kv := range pairs {
		parts[i] = fmt.Sprintf("%s=%s", kv[0], quote(kv[1]))
	}
	return strings.Join(parts, " ")
}

// quote puts a value in single quotes; \ and ' are escaped.
func quote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}
