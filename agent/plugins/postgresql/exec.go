// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

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
func (p Plugin) childEnv(password []byte) []string {
	var out []string
	for _, kv := range p.environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "PG") || name == "LC_ALL" || name == "LC_MESSAGES" {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "LC_MESSAGES=C")
	if password != nil {
		out = append(out, "PGPASSWORD="+string(password))
	}
	return out
}

// outcome is how a run of a tool ended.
type outcome struct {
	tool string
	code int
	err  error // the tool did not start or was killed by a signal
	// reason is the last line of stderr that names an error.
	reason string
}

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

// run starts a tool. Its stderr goes to the log of the step line by line,
// WARN for the lines that report an error or a warning (F1 ПГ22).
func (p Plugin) run(ctx context.Context, h sdk.Host, path string, args, environ []string, stdout io.Writer) outcome {
	o := outcome{tool: toolName(path)}
	var last, lastError string
	var mu sync.Mutex
	code, err := p.runner().Run(ctx, Cmd{Path: path, Args: args, Env: environ, Stdout: stdout, Stderr: func(line string) {
		line = strings.TrimSpace(line)
		if line == "" {
			return
		}
		h.Log(logLevel(line), line)
		mu.Lock()
		defer mu.Unlock()
		last = line
		if isError(line) {
			lastError = line
		}
	}})
	mu.Lock()
	defer mu.Unlock()
	o.code, o.err = code, err
	o.reason = cut(firstNonEmpty(lastError, last))
	return o
}

func toolName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// isError tells the lines libpq and pg_dump use for errors.
func isError(line string) bool {
	return strings.Contains(line, "error:") || strings.Contains(line, "FATAL:")
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

// output runs a tool and returns what it printed.
func (p Plugin) output(ctx context.Context, h sdk.Host, path string, args, environ []string) (string, outcome) {
	var out bytes.Buffer
	o := p.run(ctx, h, path, args, environ, &out)
	return out.String(), o
}
