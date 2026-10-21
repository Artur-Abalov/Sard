// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql/pgclient"
)

// applicationName marks the sessions of the agent on the server.
const applicationName = "sard-agent"

// target is the server the plugin signs in to and, for a restore, the tool it
// signs in with. The restore journal keeps it, without the password: only the
// name of the secret.
type target struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	PasswordRef string `json:"password_ref"`
	TLSMode     string `json:"tls_mode,omitempty"`
	TLSRootCert string `json:"tls_root_cert,omitempty"`
	ClientPath  string `json:"client_path,omitempty"`
}

// conninfo is the libpq connection string of the target (F1 ПГ4): every value
// in single quotes, so that a database named "x host=evil" is a name. The
// password is not in it.
func (t target) conninfo(dbname string) string {
	pairs := [][2]string{
		{"host", t.Host},
		{"port", strconv.Itoa(t.Port)},
		{"dbname", dbname},
		{"user", t.User},
		{"sslmode", t.sslmode()},
	}
	if t.TLSRootCert != "" {
		pairs = append(pairs, [2]string{"sslrootcert", t.TLSRootCert})
	}
	pairs = append(pairs,
		[2]string{"connect_timeout", "30"},
		[2]string{"keepalives_idle", "60"},
		[2]string{"keepalives_interval", "10"},
		[2]string{"keepalives_count", "6"},
		[2]string{"application_name", applicationName},
	)
	return pgclient.Conninfo(pairs)
}

// sslmode is the TLS mode of the connection: require for a TCP host and
// disable for a socket unless the config says otherwise.
func (t target) sslmode() string {
	switch {
	case t.TLSMode != "":
		return t.TLSMode
	case strings.HasPrefix(t.Host, "/"):
		return tlsDisable
	}
	return tlsRequire
}

// target is the server of the config of a backup.
func (c config) target() target {
	return target{Host: c.Host, Port: c.port(), User: c.User, PasswordRef: c.PasswordRef, TLSMode: c.TLSMode, TLSRootCert: c.TLSRootCert}
}

// conninfo is the connection string of the database of the config.
func (c config) conninfo() string { return c.target().conninfo(c.Database) }
