// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"io"
	"strings"
)

// conn is the connection to the target server of a restore: the parameters of
// the config, the password and the programs of the host.
type conn struct {
	p        Plugin
	log      logger
	to       target
	password []byte
	tools    tools
}

// env is the environment of a program (F1 ПГ3).
func (c conn) env() []string { return c.p.childEnv(c.password) }

// run starts a program. Its stderr goes to the log of the step line by line,
// WARN for the lines that report an error or a warning (F1 ПГ22).
func (c conn) run(ctx context.Context, path string, args []string, stdin io.Reader) outcome {
	_, o := c.p.output(ctx, c.log, call{path: path, args: args, environ: c.env(), stdin: stdin})
	return o
}

// psql runs a script on the database dbname of the target: the script is the
// standard input, so that psql quotes the variables (:'name' as a literal,
// :"name" as an identifier) and no name is ever pasted into SQL.
func (c conn) psql(ctx context.Context, dbname, script string, vars ...string) outcome {
	args := []string{"-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
	for i := 0; i+1 < len(vars); i += 2 {
		args = append(args, "-v", vars[i]+"="+vars[i+1])
	}
	args = append(args, "--dbname="+c.to.conninfo(dbname))
	return c.run(ctx, c.tools.psql, args, strings.NewReader(script))
}

// restoreProbe is what the target answered to restoreProbeScript.
type restoreProbe struct {
	server   version
	super    bool
	createdb bool
	user     string
}

// restoreProbeScript asks the target who the role is and what it may do. The
// version goes first so that a server without a column the rest needs still answers.
const restoreProbeScript = `select current_setting('server_version_num'), r.rolsuper, r.rolcreatedb, current_user from pg_roles r where r.rolname = current_user;`

// parseRestoreProbe reads the row: the right asked about is CREATEDB.
func parseRestoreProbe(o outcome) (restoreProbe, error) {
	row, err := parseProbeRow(o.last())
	if err != nil {
		return restoreProbe{}, err
	}
	return restoreProbe{server: row.server, super: row.super, createdb: row.privilege, user: row.name}, nil
}
