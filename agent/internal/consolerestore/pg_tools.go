// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// packageHint names the packages that bring the client tools.
const packageHint = "install the PostgreSQL client package (postgresql-client on Debian/Ubuntu, postgresql on RHEL/Fedora)"

// minServerMajor is the oldest server a snapshot is restored into (F1 ПГ19).
const minServerMajor = 14

// locate finds pg_restore (pg_restore_path or the PATH of the agent) and psql
// beside it (F1 ПГ8).
func (e *Engine) locate(clientPath string, environ []string) (pgTools, *Failure) {
	restore, f := e.findRestore(clientPath, environ)
	if f != nil {
		return pgTools{}, f
	}
	psql := filepath.Join(filepath.Dir(restore), "psql")
	if reason := e.runnable(psql); reason != "" {
		return pgTools{}, &Failure{Reason: refusal.PGClientMissing, Stage: StageCheck,
			Detail: fmt.Sprintf("psql not found at %q (%s), expected beside pg_restore: %s", psql, reason, packageHint)}
	}
	return pgTools{restore: restore, psql: psql}, nil
}

func (e *Engine) findRestore(clientPath string, environ []string) (string, *Failure) {
	if clientPath != "" {
		if reason := e.runnable(clientPath); reason != "" {
			return "", &Failure{Reason: refusal.PGClientMissing, Stage: StageCheck, Detail: fmt.Sprintf("pg_restore_path %q: %s; %s or correct pg_restore_path", clientPath, reason, packageHint)}
		}
		return clientPath, nil
	}
	for _, dir := range filepath.SplitList(envOf(environ, "PATH")) {
		if path := filepath.Join(dir, "pg_restore"); dir != "" && e.runnable(path) == "" {
			return path, nil
		}
	}
	return "", &Failure{Reason: refusal.PGClientMissing, Stage: StageCheck,
		Detail: "pg_restore not found in the PATH of the agent: " + packageHint + " or set pg_restore_path"}
}

// runnable says why path cannot be started, or "".
func (e *Engine) runnable(path string) string {
	info, err := e.opts.Postgres.Tools.Stat(path)
	switch {
	case err != nil:
		return reasonOf(err)
	case info.IsDir():
		return "is a directory"
	case info.Mode()&0o111 == 0:
		return "permission denied"
	}
	return ""
}

// reasonOf is the answer of the operating system without the path in it.
func reasonOf(err error) string {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return "no such file or directory"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

func envOf(environ []string, name string) string {
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// pgVersion is a PostgreSQL version; only the major number is compared.
type pgVersion struct {
	text  string // 16.4, 18beta1
	major int
}

var clientVersion = regexp.MustCompile(`\(PostgreSQL\) ((\d+)\S*)`)

// parseClient reads the version from the output of `pg_restore --version`.
func parseClient(output string) (pgVersion, bool) {
	m := clientVersion.FindStringSubmatch(output)
	if m == nil {
		return pgVersion{}, false
	}
	major, _ := strconv.Atoi(m[2]) // the pattern guarantees digits
	return pgVersion{text: m[1], major: major}, true
}

// parseTag reads the version of a tag of a snapshot ("16.4", "18beta1"); false
// for a value that does not start with a number.
func parseTag(text string) (pgVersion, bool) {
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return pgVersion{}, false
	}
	major, _ := strconv.Atoi(text[:end])
	return pgVersion{text: text, major: major}, true
}

// serverVersion reads server_version_num: 160004 is 16.4.
func serverVersion(num int) pgVersion {
	return pgVersion{text: fmt.Sprintf("%d.%d", num/10000, num%10000), major: num / 10000}
}
