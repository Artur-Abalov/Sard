// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Packages that bring the client tools, for the hints of the messages.
const packageHint = "install the PostgreSQL client package (postgresql-client on Debian/Ubuntu, postgresql on RHEL/Fedora)"

// tools are the executables of one step, all taken from one directory.
type tools struct {
	pgDump, psql string
	// pgDumpall is set by locateDumpall, when the global objects are dumped.
	pgDumpall string
}

func (p Plugin) fs() FS {
	if p.FS == nil {
		return osFS{}
	}
	return p.FS
}

// setup is what every phase needs first: the password of the secret and the
// tools of the host.
func (p Plugin) setup(h sdk.Host, c config) (password []byte, t tools, err error) {
	if password, err = p.password(h, c); err != nil {
		return nil, tools{}, err
	}
	t, err = p.locate(c)
	return password, t, err
}

// locate finds pg_dump (pg_dump_path or the PATH of the agent) and psql
// beside it (F1 ПГ8).
func (p Plugin) locate(c config) (tools, error) {
	pgDump, err := p.findPgDump(c)
	if err != nil {
		return tools{}, err
	}
	t := tools{pgDump: pgDump, psql: filepath.Join(filepath.Dir(pgDump), "psql")}
	if err := p.neighbour(t.psql, "psql", ""); err != nil {
		return tools{}, err
	}
	return t, nil
}

// locateDumpall finds pg_dumpall beside pg_dump (F1 ПГ17d). It is looked for
// once the versions are known: an old pg_dump is the first thing to tell.
func (p Plugin) locateDumpall(t tools) (tools, error) {
	t.pgDumpall = filepath.Join(filepath.Dir(t.pgDump), "pg_dumpall")
	if err := p.neighbour(t.pgDumpall, "pg_dumpall", "; or set include_globals to false"); err != nil {
		return tools{}, err
	}
	return t, nil
}

func (p Plugin) findPgDump(c config) (string, error) {
	if c.PgDumpPath == "" {
		return p.searchPath()
	}
	if reason := p.runnable(c.PgDumpPath); reason != "" {
		return "", fmt.Errorf("pg_dump_path %q: %s", c.PgDumpPath, reason)
	}
	return c.PgDumpPath, nil
}

// searchPath looks for pg_dump in the PATH of the agent.
func (p Plugin) searchPath() (string, error) {
	for _, dir := range filepath.SplitList(env(p.environ(), "PATH")) {
		if dir == "" {
			continue
		}
		if path := filepath.Join(dir, "pg_dump"); p.runnable(path) == "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("pg_dump not found in the PATH of the agent: %s or set pg_dump_path", packageHint)
}

// neighbour checks a tool that must lie beside pg_dump.
func (p Plugin) neighbour(path, name, hint string) error {
	if reason := p.runnable(path); reason != "" {
		return fmt.Errorf("%s not found at %q (%s), expected beside pg_dump: %s%s", name, path, reason, packageHint, hint)
	}
	return nil
}

// runnable says why path cannot be started, or "".
func (p Plugin) runnable(path string) string {
	info, err := p.fs().Stat(path)
	switch {
	case err != nil:
		return reason(err)
	case info.IsDir():
		return "is a directory"
	case info.Mode()&0o111 == 0:
		return "permission denied"
	}
	return ""
}

// reason is the operating system's answer without the path in it.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return "no such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// env is the value of a variable of an environment, "" if it is not set.
func env(environ []string, name string) string {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}
