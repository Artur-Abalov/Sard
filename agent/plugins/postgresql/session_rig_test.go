// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
)

// fakeSession plays the psql of the transaction of the fingerprint: it reads
// the statements up to each \echo, answers the queries it knows with a row
// and prints the marker. A statement it is told to fail prints no row and an
// ERROR on stderr, as psql does with ON_ERROR_STOP=0.
type fakeSession struct {
	mu sync.Mutex
	// rows answers a query whose text holds the key; the first key found wins,
	// in the order of keys.
	keys []string
	rows map[string]string
	// failing are the keys whose queries fail.
	failing map[string]bool
	// sent is every request, in order; exitAfter ends psql after that many
	// requests (0: never), with exitCode and exitLine on stderr.
	sent      []string
	exitAfter int
	exitCode  int
	exitLine  string
	// hold, if set, is closed by the test to let psql exit at the end of its input.
	hold chan struct{}
}

const fakeSnapshot = "00000003-0000001B-1"

func newSession() *fakeSession {
	s := &fakeSession{rows: map[string]string{}, failing: map[string]bool{}}
	s.answer("transaction_isolation", "repeatable read on")
	s.answer("pg_export_snapshot", fakeSnapshot)
	s.answer("server_version_num", "180001 18.1")
	s.answer("pg_extension", `[{"n":"plpgsql","v":"1.0"},{"n":"pg_trgm","v":"1.6"}]`)
	s.answer("pg_database_size", "81920")
	s.answer("json_build_object('s'", `[{"s":"public","t":"orders","b":300},{"s":"public","t":"users","b":200},{"s":"app","t":"x\"y","b":100}]`)
	s.answer(`FROM "public"."orders"`, "42")
	s.answer(`FROM "public"."users"`, "7")
	s.answer(`FROM "app"."x""y"`, "1")
	return s
}

func (s *fakeSession) answer(key, row string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.rows[key] = row
}

func (s *fakeSession) fail(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[key] = true
}

func (s *fakeSession) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

// reply is the row of a request, if any.
func (s *fakeSession) reply(req string) (string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, req)
	for _, k := range s.keys {
		if strings.Contains(req, k) {
			return s.rows[k], !s.failing[k], s.exitAfter > 0 && len(s.sent) >= s.exitAfter
		}
	}
	return "", false, s.exitAfter > 0 && len(s.sent) >= s.exitAfter
}

func (s *fakeSession) behaviour() behaviour {
	return func(ctx context.Context, c plugCmd) (int, error) {
		in := bufio.NewScanner(c.Stdin)
		var req strings.Builder
		for in.Scan() {
			marker, isEcho := strings.CutPrefix(in.Text(), `\echo `)
			if !isEcho {
				req.WriteString(in.Text() + "\n")
				continue
			}
			if code, exit := s.respond(c, req.String(), marker); exit {
				return code, nil
			}
			req.Reset()
		}
		return s.wait(ctx)
	}
}

// respond answers one request; true when psql exits after it.
func (s *fakeSession) respond(c plugCmd, req, marker string) (int, bool) {
	row, ok, exit := s.reply(req)
	switch {
	case row != "" && ok:
		_, _ = io.WriteString(c.Stdout, "R"+row+"\n")
	case row != "" && c.Stderr != nil:
		c.Stderr("ERROR:  canceling statement due to statement timeout")
	}
	if exit {
		if c.Stderr != nil && s.exitLine != "" {
			c.Stderr(s.exitLine)
		}
		return s.exitCode, true
	}
	_, _ = io.WriteString(c.Stdout, marker+"\n")
	return 0, false
}

// wait holds psql after the end of its input, while the test says so.
func (s *fakeSession) wait(ctx context.Context) (int, error) {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold == nil {
		return 0, nil
	}
	select {
	case <-hold:
		return 0, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
