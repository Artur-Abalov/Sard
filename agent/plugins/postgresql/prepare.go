// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// probe is the query of PREPARING: the version of the server and what the
// role may do. to_regrole keeps it valid on a server without pg_read_all_data.
const probeQuery = `select current_setting('server_version_num'), r.rolsuper, ` +
	`coalesce(pg_has_role(current_user, to_regrole('pg_read_all_data')::oid, 'member'), false), current_user ` +
	`from pg_roles r where r.rolname = current_user`

// checked is what PREPARING learned and DUMPING needs.
type checked struct {
	server, pgDump version
	pgDumpall      version
}

// prepared holds the results of Prepare until Dump takes them, per step.
var prepared sync.Map // sdk.Host → checked

// Prepare implements sdk.Plugin: the secret, the tools, their versions, the
// connection and the role (F1 ПГ1–ПГ8, ПГ17c, ПГ17d).
func (p Plugin) Prepare(ctx context.Context, h sdk.Host, cfg sdk.Config) error {
	c, err := parse(cfg)
	if err != nil {
		return err
	}
	password, t, err := p.setup(h, c)
	if err != nil {
		return err
	}
	done, err := p.check(ctx, h, c, t, password)
	if err != nil {
		return err
	}
	prepared.Store(h, done)
	return nil
}

// check asks the tools and the server what the dump needs to know.
func (p Plugin) check(ctx context.Context, h sdk.Host, c config, t tools, password []byte) (checked, error) {
	pgDump, err := p.toolVersion(ctx, h, t.pgDump)
	if err != nil {
		return checked{}, err
	}
	role, err := p.probe(ctx, h, c, t, password)
	if err != nil {
		return checked{}, err
	}
	if err := checkVersions(pgDump, role.server); err != nil {
		return checked{}, err
	}
	if err := checkRole(h, c, role); err != nil {
		return checked{}, err
	}
	done := checked{server: role.server, pgDump: pgDump}
	if c.globals() {
		done.pgDumpall, err = p.checkPgDumpall(ctx, h, t, role.server)
	}
	return done, err
}

// password reads the secret of the config: its content without the line
// ending, neither empty nor holding a NUL byte (F1 ПГ3).
func (p Plugin) password(h sdk.Host, c config) ([]byte, error) {
	value, err := h.Secret(c.PasswordRef)
	if err != nil {
		return nil, err
	}
	value = bytes.TrimRight(value, "\r\n")
	switch {
	case len(value) == 0:
		return nil, fmt.Errorf("secret %q is empty", c.PasswordRef)
	case bytes.IndexByte(value, 0) >= 0:
		return nil, fmt.Errorf("secret %q contains a NUL byte", c.PasswordRef)
	}
	return value, nil
}

// toolVersion runs `<tool> --version`.
func (p Plugin) toolVersion(ctx context.Context, h sdk.Host, path string) (version, error) {
	out, o := p.output(ctx, h, path, []string{"--version"}, p.childEnv(nil))
	if err := o.failure(); err != nil {
		return version{}, err
	}
	return parseClient(o.tool, out)
}

// role is what the server told about the connection.
type role struct {
	server    version
	name      string
	super     bool
	readsData bool
}

// probe signs in with psql and asks the server about itself and the role.
func (p Plugin) probe(ctx context.Context, h sdk.Host, c config, t tools, password []byte) (role, error) {
	args := []string{"-X", "-w", "-A", "-t", "-v", "ON_ERROR_STOP=1", "--dbname=" + c.conninfo(), "-c", probeQuery}
	out, o := p.output(ctx, h, t.psql, args, p.childEnv(password))
	if ctx.Err() != nil {
		return role{}, context.Cause(ctx)
	}
	if err := o.failure(); err != nil {
		return role{}, err
	}
	return parseProbe(out)
}

// parseProbe reads the row of the probe: version number, superuser, member of
// pg_read_all_data, role (last, it may hold any character).
func parseProbe(out string) (role, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f := strings.SplitN(line, "|", 4)
	if len(f) != 4 {
		return role{}, fmt.Errorf("unexpected answer of psql: %q", line)
	}
	num, err := strconv.Atoi(f[0])
	if err != nil {
		return role{}, fmt.Errorf("unexpected answer of psql: %q", line)
	}
	return role{server: serverVersion(num), super: f[1] == "t", readsData: f[2] == "t", name: f[3]}, nil
}

// checkVersions: the server is supported and pg_dump is not older (F1 ПГ7).
func checkVersions(pgDump, server version) error {
	if server.major < minServerMajor {
		return fmt.Errorf("PostgreSQL %s is not supported: the oldest supported server is %d", server.text, minServerMajor)
	}
	if pgDump.major < server.major {
		return fmt.Errorf("pg_dump %s is older than the PostgreSQL server %s: install pg_dump %d or newer (%s) or set pg_dump_path",
			pgDump.text, server.text, server.major, packageHint)
	}
	return nil
}

// checkRole fails a request for role passwords without a superuser and warns
// about a role that may not read every table (F1 ПГ2, ПГ17c).
func checkRole(h sdk.Host, c config, r role) error {
	if c.globals() && c.GlobalsRolePasswords && !r.super {
		return fmt.Errorf("role %q is not a superuser: globals_role_passwords needs a superuser", r.name)
	}
	if !r.super && !r.readsData {
		h.Log(sdk.LevelWarn, fmt.Sprintf("role %q is not a superuser and not a member of pg_read_all_data: pg_dump fails on a table it cannot read", r.name))
	}
	return nil
}

// checkPgDumpall checks the version of pg_dumpall like that of pg_dump.
func (p Plugin) checkPgDumpall(ctx context.Context, h sdk.Host, t tools, server version) (version, error) {
	t, err := p.locateDumpall(t)
	if err != nil {
		return version{}, err
	}
	v, err := p.toolVersion(ctx, h, t.pgDumpall)
	if err != nil {
		return version{}, err
	}
	if v.major < server.major {
		return version{}, fmt.Errorf("pg_dumpall %s is older than the PostgreSQL server %s: install pg_dumpall %d or newer (%s) or set pg_dump_path",
			v.text, server.text, server.major, packageHint)
	}
	return v, nil
}

var errNotPrepared = errors.New("the step was not prepared")
