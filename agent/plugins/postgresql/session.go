// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// session is one psql that runs queries in one transaction, REPEATABLE READ
// and READ ONLY, as long as the plugin needs it: the fingerprint and pg_dump
// --snapshot see the state that transaction exported (V-D3). Each query is
// sent on psql's standard input with a marker after it; its row comes back on
// the standard output, prefixed with R. A query runs under a savepoint, so
// that a failure (a statement timeout) does not end the transaction.
type session struct {
	in    *os.File
	lines chan string
	// done is closed when psql has exited; result says how.
	done   chan struct{}
	result outcome
	stop   context.CancelFunc
	grace  time.Duration
	once   sync.Once
}

// sessionGrace is how long psql may take to roll back and exit once its input ends.
const sessionGrace = 10 * time.Second

// errSessionEnded: psql exited while the plugin still needed the transaction.
var errSessionEnded = errors.New("the psql session of the fingerprint ended")

// openSession starts psql on the conninfo and opens the transaction.
func (p Plugin) openSession(ctx context.Context, log logger, psql, conninfo string, password []byte) (*session, error) {
	// A pipe of the system, not io.Pipe: psql gets the descriptor itself, so
	// that no goroutine of exec copies the input and outlives psql.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("psql session: %w", err)
	}
	runCtx, stop := context.WithCancel(ctx)
	s := &session{in: w, lines: make(chan string, 16), done: make(chan struct{}), stop: stop, grace: sessionGrace}
	args := []string{"-X", "-w", "-A", "-t", "-q", "-v", "ON_ERROR_STOP=0", "--dbname=" + conninfo}
	go func() {
		s.result = p.run(runCtx, log, call{path: psql, args: args, environ: p.childEnv(password), stdin: r, stdout: &lineSink{lines: s.lines}})
		_ = r.Close()
		close(s.lines)
		close(s.done)
	}()
	if err := s.begin(ctx); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// beginQuery opens the transaction and says what it is.
const beginQuery = "\\encoding UTF8\nSET standard_conforming_strings = on;\nBEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\n" +
	"SET LOCAL idle_in_transaction_session_timeout = 0;\nSET LOCAL statement_timeout = 0;\n" +
	"SELECT 'R' || current_setting('transaction_isolation') || ' ' || current_setting('transaction_read_only');"

// begin opens the transaction: REPEATABLE READ, READ ONLY, no idle timeout.
func (s *session) begin(ctx context.Context) error {
	got, ok, err := s.ask(ctx, beginQuery)
	if err != nil {
		return err
	}
	if !ok || got != "repeatable read on" {
		return fmt.Errorf("psql did not open the transaction of the fingerprint: %q", got)
	}
	return nil
}

// query runs one statement that prints at most one row under a savepoint;
// false when it printed none: it failed, its error is in the log of the step.
func (s *session) query(ctx context.Context, sql string) (string, bool, error) {
	got, ok, err := s.ask(ctx, "SAVEPOINT sard_q;\n"+sql)
	if err != nil {
		return "", false, err
	}
	end := "RELEASE SAVEPOINT sard_q;\n"
	if !ok {
		end = "ROLLBACK TO SAVEPOINT sard_q;\n"
	}
	if err := s.send(end); err != nil {
		return "", false, err
	}
	return got, ok, nil
}

// ask sends statements and a marker and reads the output up to the marker: the
// first row it printed, if any.
func (s *session) ask(ctx context.Context, sql string) (string, bool, error) {
	marker := "M" + newMarker()
	if err := s.send(sql + "\n\\echo " + marker + "\n"); err != nil {
		return "", false, err
	}
	return s.readUntil(ctx, marker)
}

// readUntil reads the output up to the marker: its first row, if any.
func (s *session) readUntil(ctx context.Context, marker string) (string, bool, error) {
	var a answer
	for {
		select {
		case <-ctx.Done():
			return "", false, context.Cause(ctx)
		case line, open := <-s.lines:
			if !open {
				return "", false, s.ended()
			}
			if line == marker {
				return a.row, a.got, nil
			}
			a.read(line)
		}
	}
}

// answer is the first row a request printed.
type answer struct {
	row string
	got bool
}

func (a *answer) read(line string) {
	if row, ok := strings.CutPrefix(line, "R"); ok && !a.got {
		a.row, a.got = row, true
	}
}

func (s *session) send(text string) error {
	if _, err := io.WriteString(s.in, text); err != nil {
		return s.ended()
	}
	return nil
}

// ended is the error of a session whose psql exited.
func (s *session) ended() error {
	<-s.done
	if why := s.result.text(); why != "" {
		return fmt.Errorf("%w: %s", errSessionEnded, why)
	}
	return errSessionEnded
}

// alive reports whether psql still runs.
func (s *session) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// close ends the transaction and psql: the input ends, psql rolls back and
// exits; one that does not within the grace is stopped. It is safe to call
// more than once.
func (s *session) close() {
	s.once.Do(func() {
		_, _ = io.WriteString(s.in, "ROLLBACK;\n")
		_ = s.in.Close()
		t := time.NewTimer(s.grace)
		defer t.Stop()
		select {
		case <-s.done:
		case <-t.C:
		}
		s.stop()
		<-s.done
	})
}

// newMarker is a token no row of a query prints.
func newMarker() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// lineSink passes the lines of psql's standard output to the session.
type lineSink struct {
	lines chan<- string
	buf   []byte
}

func (l *lineSink) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		line, rest, found := bytes.Cut(l.buf, []byte{'\n'})
		if !found {
			return len(p), nil
		}
		l.lines <- strings.TrimSuffix(string(line), "\r")
		l.buf = rest
	}
}
