// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk/pgclient"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// maxQuoteBytes keeps the quote of an error of a program short.
const maxQuoteBytes = 512

// pgConn is the connection to the target server: the parameters of the plan,
// the password and the programs of the host.
type pgConn struct {
	plan     sdk.PostgresRestore
	password []byte
	tools    pgTools
	environ  func() []string
	exec     restic.Executor
}

// pgTools are the programs of one restore, all of one directory.
type pgTools struct{ restore, psql string }

// conninfo is the libpq connection string of the target (F1 ПГ4): every value
// in single quotes, so that a database named "x host=evil" is a name. The
// password is not in it.
func (c pgConn) conninfo(dbname string) string {
	pairs := [][2]string{
		{"host", c.plan.Host}, {"port", strconv.Itoa(c.plan.Port)}, {"dbname", dbname},
		{"user", c.plan.User}, {"sslmode", c.sslmode()},
	}
	if c.plan.TLSRootCert != "" {
		pairs = append(pairs, [2]string{"sslrootcert", c.plan.TLSRootCert})
	}
	pairs = append(pairs, [2]string{"connect_timeout", "30"}, [2]string{"keepalives_idle", "60"},
		[2]string{"keepalives_interval", "10"}, [2]string{"keepalives_count", "6"}, [2]string{"application_name", "sard-agent"})
	return pgclient.Conninfo(pairs)
}

// sslmode is the TLS mode of the plan: require for a TCP host, disable for a
// socket directory unless the plan says otherwise.
func (c pgConn) sslmode() string {
	switch {
	case c.plan.TLSMode != "":
		return c.plan.TLSMode
	case strings.HasPrefix(c.plan.Host, "/"):
		return "disable"
	}
	return "require"
}

// env is the environment of a program (F1 ПГ3).
func (c pgConn) env() []string { return pgclient.Env(c.environ(), c.password) }

// outcome is how a run of a program ended.
type outcome struct {
	tool   string
	code   int
	err    error // the program did not start or was killed by a signal
	stdout []string
	// reason is the line of stderr that names the error.
	reason string
}

func (o outcome) ok() bool { return o.err == nil && o.code == 0 }

// text is the failure of the run in a line: "psql failed (exit code 3): <quote>".
func (o outcome) text() string {
	switch {
	case o.err != nil:
		return fmt.Sprintf("%s failed: %v", o.tool, o.err)
	case o.reason != "":
		return fmt.Sprintf("%s failed (exit code %d): %s", o.tool, o.code, o.reason)
	}
	return fmt.Sprintf("%s failed (exit code %d)", o.tool, o.code)
}

// why is the quote of the error of a failed run, or the failure in words.
func (o outcome) why() string {
	if o.reason != "" {
		return o.reason
	}
	return o.text()
}

// problem is what to tell of a run that failed or answered with something the
// agent cannot read.
func (o outcome) problem() string {
	if !o.ok() {
		return o.text()
	}
	return fmt.Sprintf("unexpected answer of psql: %q", o.last())
}

// last is the last line the program printed, "" if none.
func (o outcome) last() string {
	for i := len(o.stdout) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(o.stdout[i]); line != "" {
			return line
		}
	}
	return ""
}

// run starts a program. Its stderr goes to the log of the step line by line,
// WARN for the lines that report an error or a warning (F1 ПГ22).
func (c pgConn) run(ctx context.Context, r Reporter, path string, args []string, stdin io.Reader) outcome {
	name := path[strings.LastIndex(path, "/")+1:]
	o := outcome{tool: name}
	q := &quoter{}
	var mu sync.Mutex
	code, err := c.exec.Run(ctx, restic.Command{
		Path: path, Args: args, Env: c.env(), Stdin: stdin,
		Stdout: func(line []byte) {
			mu.Lock()
			o.stdout = append(o.stdout, string(line))
			mu.Unlock()
		},
		Stderr: func(line []byte) {
			text := strings.TrimSpace(string(line))
			if text == "" {
				return
			}
			r.Log(logLevel(text), text)
			q.add(text)
		},
	})
	o.code, o.err, o.reason = code, err, q.reason()
	return o
}

// psql runs a script on the database dbname of the target: the script is the
// standard input, so that psql quotes the variables (:'name' as a literal,
// :"name" as an identifier) and no name is ever pasted into SQL.
func (c pgConn) psql(ctx context.Context, r Reporter, dbname, script string, vars ...string) outcome {
	args := []string{"-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
	for i := 0; i+1 < len(vars); i += 2 {
		args = append(args, "-v", vars[i]+"="+vars[i+1])
	}
	args = append(args, "--dbname="+c.conninfo(dbname))
	return c.run(ctx, r, c.tools.psql, args, strings.NewReader(script))
}

// quoter picks, from the stderr of a program, the line that names the error.
type quoter struct {
	mu          sync.Mutex
	last, error string
}

func (q *quoter) add(line string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.last = line
	if isErrorLine(line) {
		q.error = line
	}
}

func (q *quoter) reason() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	line := q.error
	if line == "" {
		line = q.last
	}
	if len(line) > maxQuoteBytes {
		return strings.ToValidUTF8(line[:maxQuoteBytes], "") + "..."
	}
	return line
}

// isErrorLine tells the lines libpq, psql and pg_restore use for errors.
func isErrorLine(line string) bool {
	return strings.Contains(line, "error:") || strings.Contains(line, "FATAL:") || strings.Contains(line, "ERROR:")
}

func logLevel(line string) agentv1.LogLevel {
	if isErrorLine(line) || strings.Contains(line, "warning:") {
		return agentv1.LogLevel_LOG_LEVEL_WARN
	}
	return agentv1.LogLevel_LOG_LEVEL_INFO
}
