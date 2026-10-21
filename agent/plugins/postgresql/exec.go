// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql/pgclient"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// logger is where the lines of the tools go: the host of a step, or that of a
// recovery at the start of the agent.
type logger interface {
	Log(level sdk.Level, text string)
}

// maxQuoteBytes keeps the quote of a tool's error short.
const maxQuoteBytes = 512

func (p Plugin) runner() Runner {
	if p.Runner == nil {
		return ProcessRunner{}
	}
	return p.Runner
}

func (p Plugin) environ() []string {
	if p.Environ == nil {
		return os.Environ()
	}
	return p.Environ()
}

// childEnv is the environment of psql, pg_dump and pg_dumpall (F1 ПГ3): the
// environment of the agent without the PG* variables and LC_ALL, with
// messages in English and the password, if there is one.
func (p Plugin) childEnv(password []byte) []string { return pgclient.Env(p.environ(), password) }

// call is one run of a tool.
type call struct {
	path    string
	args    []string
	environ []string
	// stdin and stdout are the standard input and output; nil: empty and discarded.
	stdin  io.Reader
	stdout io.Writer
}

// outcome is how a run of a tool ended.
type outcome struct {
	tool string
	code int
	err  error // the tool did not start or was killed by a signal
	// reason is the last line of stderr that names an error.
	reason string
	// out is what the tool printed, when the caller asked for it (output).
	out string
}

// ok: the tool started and ended with 0.
func (o outcome) ok() bool { return o.err == nil && o.code == 0 }

// failure is the error of a run that did not succeed, nil otherwise.
func (o outcome) failure() error {
	switch {
	case o.err != nil:
		return fmt.Errorf("%s failed: %w", o.tool, o.err)
	case o.code != 0 && o.reason != "":
		return fmt.Errorf("%s failed (exit code %d): %s", o.tool, o.code, o.reason)
	case o.code != 0:
		return fmt.Errorf("%s failed (exit code %d)", o.tool, o.code)
	}
	return nil
}

// text is the failure of the run in a line: "psql failed (exit code 3): <quote>".
func (o outcome) text() string {
	if err := o.failure(); err != nil {
		return err.Error()
	}
	return ""
}

// why is the quote of the error of a failed run, or the failure in words.
func (o outcome) why() string { return firstNonEmpty(o.reason, o.text()) }

// problem is what to tell of a run that failed or answered with something the
// plugin cannot read.
func (o outcome) problem() string {
	if !o.ok() {
		return o.text()
	}
	return fmt.Sprintf("unexpected answer of psql: %q", o.last())
}

// lines are the lines the tool printed.
func (o outcome) lines() []string {
	lines := strings.Split(strings.TrimSuffix(o.out, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// last is the last line the tool printed that is not blank, "" if none.
func (o outcome) last() string {
	lines := o.lines()
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// run starts a tool. Its stderr goes to the log of the step line by line,
// WARN for the lines that report an error or a warning (F1 ПГ22).
func (p Plugin) run(ctx context.Context, log logger, c call) outcome {
	o := outcome{tool: filepath.Base(c.path)}
	quote := &quoter{detailPrefix: o.tool + ": detail: "}
	code, err := p.runner().Run(ctx, Cmd{Path: c.path, Args: c.args, Env: c.environ, Stdin: c.stdin, Stdout: c.stdout, Stderr: func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		log.Log(logLevel(line), line)
		quote.add(line)
	}})
	o.code, o.err, o.reason = code, err, quote.reason()
	return o
}

// quoter picks, from the stderr of a tool, the line that names the error and
// the first line "<tool>: detail: …" after it.
type quoter struct {
	detailPrefix string

	mu                  sync.Mutex
	last, errLine, more string
}

func (q *quoter) add(line string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.last = line
	switch {
	case isError(line):
		q.errLine, q.more = line, ""
	case q.errLine != "" && q.more == "" && strings.HasPrefix(line, q.detailPrefix):
		q.more = strings.TrimPrefix(line, q.detailPrefix)
	}
}

// reason is the quote of the error, cut to a length, and its detail, if any,
// cut separately: "<quote> — detail: <text>".
func (q *quoter) reason() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	quote := cut(firstNonEmpty(q.errLine, q.last))
	if q.more == "" {
		return quote
	}
	return quote + " — detail: " + cut(q.more)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// isError tells the lines libpq, psql, pg_dump and pg_restore use for errors.
func isError(line string) bool {
	return strings.Contains(line, "error:") || strings.Contains(line, "FATAL:") || strings.Contains(line, "ERROR:")
}

func logLevel(line string) sdk.Level {
	if isError(line) || strings.Contains(line, "warning:") {
		return sdk.LevelWarn
	}
	return sdk.LevelInfo
}

func cut(s string) string {
	if len(s) > maxQuoteBytes {
		return strings.ToValidUTF8(s[:maxQuoteBytes], "") + "..."
	}
	return s
}

// output runs a tool and returns what it printed, which is also in the outcome.
func (p Plugin) output(ctx context.Context, log logger, c call) (string, outcome) {
	var out bytes.Buffer
	c.stdout = &out
	o := p.run(ctx, log, c)
	o.out = out.String()
	return o.out, o
}
