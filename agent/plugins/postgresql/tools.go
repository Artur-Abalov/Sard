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
	// pgRestore is set by locateRestore: a restore needs it, a backup does not.
	pgRestore string
}

func (p Plugin) fs() FS {
	if p.FS == nil {
		return osFS{}
	}
	return p.FS
}

// setup is what every phase needs first: the password of the secret, the
// config with what its binding allows, and the tools of the host.
func (p Plugin) setup(h sdk.Host, c config) (config, []byte, tools, error) {
	c, password, err := p.password(h, c)
	if err != nil {
		return c, nil, tools{}, err
	}
	t, err := p.locate(c)
	return c, password, t, err
}

// locate finds pg_dump (pg_dump_path or the PATH of the agent) and psql
// beside it (F1 ПГ8).
func (p Plugin) locate(c config) (tools, error) {
	pgDump, err := p.findPgDump(c)
	if err != nil {
		return tools{}, err
	}
	t := tools{pgDump: pgDump, psql: filepath.Join(filepath.Dir(pgDump), "psql")}
	if err := p.neighbour(t.psql, "psql", "pg_dump", ""); err != nil {
		return tools{}, err
	}
	return t, nil
}

// locatePsql finds psql alone, for a fingerprint: beside pg_dump_path when the
// config names one (pg_dump itself need not be there: a verifier has pg_restore
// and psql), else in the PATH of the agent.
func (p Plugin) locatePsql(c config) (string, error) {
	if c.PgDumpPath != "" {
		psql := filepath.Join(filepath.Dir(c.PgDumpPath), "psql")
		return psql, p.neighbour(psql, "psql", "pg_dump_path", "")
	}
	if path := p.lookPath("psql"); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("psql %w in the PATH of the agent: %s or set pg_dump_path", sdk.ErrProgramMissing, packageHint)
}

// locateDumpall finds pg_dumpall beside pg_dump (F1 ПГ17d). It is looked for
// once the versions are known: an old pg_dump is the first thing to tell.
func (p Plugin) locateDumpall(t tools) (tools, error) {
	t.pgDumpall = filepath.Join(filepath.Dir(t.pgDump), "pg_dumpall")
	if err := p.neighbour(t.pgDumpall, "pg_dumpall", "pg_dump", "; or set include_globals to false"); err != nil {
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
	if path := p.lookPath("pg_dump"); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("pg_dump %w in the PATH of the agent: %s or set pg_dump_path", sdk.ErrProgramMissing, packageHint)
}

// lookPath finds the tool in the PATH of the agent; "" if it is not there. An
// empty entry of the PATH is not the current directory.
func (p Plugin) lookPath(tool string) string {
	for _, dir := range filepath.SplitList(env(p.environ(), "PATH")) {
		if dir == "" {
			continue
		}
		if path := filepath.Join(dir, tool); p.runnable(path) == "" {
			return path
		}
	}
	return ""
}

// neighbour checks a tool that must lie beside another one, the anchor.
func (p Plugin) neighbour(path, name, anchor, hint string) error {
	if reason := p.runnable(path); reason != "" {
		return fmt.Errorf("%s %w at %q (%s), expected beside %s: %s%s", name, sdk.ErrProgramMissing, path, reason, anchor, packageHint, hint)
	}
	return nil
}

// locateRestore finds pg_restore (pg_restore_path or the PATH of the agent) and
// psql beside it, for a restore (F1 ПГ8, R2b ВК22). Whatever is missing is
// PG_CLIENT_MISSING, with the sentinel of the SDK for the hint of check.
func (p Plugin) locateRestore(clientPath string) (tools, *sdk.RestoreError) {
	pgRestore, f := p.findPgRestore(clientPath)
	if f != nil {
		return tools{}, f
	}
	psql := filepath.Join(filepath.Dir(pgRestore), "psql")
	if err := p.neighbour(psql, "psql", "pg_restore", ""); err != nil {
		return tools{}, clientMissing(err.Error(), err)
	}
	return tools{pgRestore: pgRestore, psql: psql}, nil
}

func (p Plugin) findPgRestore(clientPath string) (string, *sdk.RestoreError) {
	if clientPath != "" {
		if reason := p.runnable(clientPath); reason != "" {
			return "", clientMissing(fmt.Sprintf("pg_restore_path %q: %s; %s or correct pg_restore_path", clientPath, reason, packageHint), sdk.ErrProgramMissing)
		}
		return clientPath, nil
	}
	if path := p.lookPath("pg_restore"); path != "" {
		return path, nil
	}
	return "", clientMissing("pg_restore not found in the PATH of the agent: "+packageHint+" or set pg_restore_path", sdk.ErrProgramMissing)
}

func clientMissing(detail string, cause error) *sdk.RestoreError {
	return &sdk.RestoreError{Reason: ReasonClientMissing, Stage: stageCheck, Detail: detail, Err: cause}
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
