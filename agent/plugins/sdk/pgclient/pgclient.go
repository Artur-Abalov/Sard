// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

// Package pgclient is the one place that knows how the agent talks to a
// PostgreSQL client program: the connection string and the environment of
// psql, pg_dump, pg_dumpall and pg_restore (F1 ПГ3, ПГ4). The plugin and the
// restore engine of the agent both use it.
package pgclient

import (
	"strings"
)

// Conninfo is the libpq connection string of the pairs, in order: every value
// in single quotes, so that a database named "x host=evil" is a name. The
// password does not belong in it.
func Conninfo(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, kv := range pairs {
		parts[i] = kv[0] + "=" + quote(kv[1])
	}
	return strings.Join(parts, " ")
}

// quote puts a value in single quotes; \ and ' are escaped.
func quote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// Env is the environment of a client program: environ without its PG*
// variables, LC_ALL and LC_MESSAGES, messages in English, and the password in
// PGPASSWORD if there is one (nil: none).
func Env(environ []string, password []byte) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !dropped(name) {
			out = append(out, kv)
		}
	}
	out = append(out, "LC_MESSAGES=C")
	if password != nil {
		out = append(out, "PGPASSWORD="+string(password))
	}
	return out
}

func dropped(name string) bool {
	return strings.HasPrefix(name, "PG") || name == "LC_ALL" || name == "LC_MESSAGES"
}
