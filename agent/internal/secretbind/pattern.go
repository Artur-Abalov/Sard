// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package secretbind is the binding of a secret of the agent to a plugin and
// to the targets its value may go to (S11, ADR 00XX-draft-secret-binding):
// the operator of the host decides where a password is sent, not the server.
package secretbind

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// wildcard is the wildcard of a port or a database.
const wildcard = "*"

// Limits of a pattern, as the schema of the postgresql plugin has them.
const (
	maxHostBytes     = 255
	maxDatabaseBytes = 63
)

// Pattern is one target of a binding: host[:port][/database], an IPv6
// address in brackets, or a socket directory /dir[:port[/database]]. A port
// or a database left out, or *, is any.
type Pattern struct {
	host, port, database string
}

// ParsePattern reads a pattern; the error says what is wrong with it.
func ParsePattern(s string) (Pattern, error) {
	if s == "" {
		return Pattern{}, errors.New("a target is empty: want host[:port][/database]")
	}
	var p Pattern
	var err error
	switch {
	case strings.HasPrefix(s, "/"):
		p, err = parseSocket(s)
	case strings.HasPrefix(s, "["):
		p, err = parseBracketed(s)
	default:
		p, err = parseHost(s)
	}
	if err != nil {
		return Pattern{}, fmt.Errorf("target %q: %w", s, err)
	}
	return p, nil
}

// parseSocket is /dir[:port[/database]]: the last colon ends the directory.
func parseSocket(s string) (Pattern, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return finish(Pattern{host: s})
	}
	return parseTail(s[:i], s[i:])
}

// parseBracketed is [ipv6][:port][/database].
func parseBracketed(s string) (Pattern, error) {
	end := strings.Index(s, "]")
	if end < 0 {
		return Pattern{}, errors.New("an IPv6 address needs its closing brackets ]")
	}
	host, rest := s[1:end], s[end+1:]
	if err := checkBracketed(host, rest); err != nil {
		return Pattern{}, err
	}
	return parseTail(host, rest)
}

// checkBracketed: an IPv6 address in the brackets, :port or /database after.
func checkBracketed(host, rest string) error {
	if ip := net.ParseIP(host); ip == nil || !strings.Contains(host, ":") {
		return errors.New("only an IPv6 address goes in brackets: want [address]:port/database")
	}
	if rest != "" && !strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "/") {
		return errors.New("after the brackets of an IPv6 address come :port or /database")
	}
	return nil
}

// parseHost is host[:port][/database], or an IPv6 address alone.
func parseHost(s string) (Pattern, error) {
	if ip := net.ParseIP(s); ip != nil {
		return Pattern{host: s}, nil
	}
	hostport, _, _ := strings.Cut(s, "/")
	host, _, _ := strings.Cut(hostport, ":")
	if strings.Count(hostport, ":") > 1 {
		return Pattern{}, errors.New("an IPv6 address goes in brackets: [address]:port/database")
	}
	if err := checkHost(host); err != nil {
		return Pattern{}, err
	}
	return parseTail(host, s[len(host):])
}

// parseTail reads [:port][/database] after the host.
func parseTail(host, rest string) (Pattern, error) {
	p := Pattern{host: host}
	database, hasDatabase := "", false
	switch {
	case strings.HasPrefix(rest, ":"):
		p.port, database, hasDatabase = strings.Cut(rest[1:], "/")
		if p.port == "" {
			return Pattern{}, errors.New("the port after : is empty")
		}
	case strings.HasPrefix(rest, "/"):
		database, hasDatabase = rest[1:], true
	}
	if hasDatabase && database == "" {
		return Pattern{}, errors.New("the database after / is empty")
	}
	p.database = database
	return finish(p)
}

// finish checks the host length, the port and the database of p.
func finish(p Pattern) (Pattern, error) {
	if err := checkHostBytes(p.host); err != nil {
		return Pattern{}, err
	}
	if err := checkPort(p.port); err != nil {
		return Pattern{}, err
	}
	if err := checkDatabase(p.database); err != nil {
		return Pattern{}, err
	}
	return p, nil
}

// checkHost: a name or an IPv4 address, without the characters libpq or a
// pattern would read otherwise; no wildcard.
func checkHost(host string) error {
	if host == "" || strings.ContainsAny(host, ",@*[]\x00") || strings.IndexFunc(host, isSpace) >= 0 {
		return fmt.Errorf("the host must be a name or an address, without , @ * [ ] or spaces; got %q", host)
	}
	return checkHostBytes(host)
}

func checkHostBytes(host string) error {
	if len(host) > maxHostBytes || strings.ContainsRune(host, 0) {
		return fmt.Errorf("the host is longer than %d bytes or holds a NUL byte", maxHostBytes)
	}
	return nil
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

// checkPort: empty, * or 1-65535.
func checkPort(port string) error {
	if port == "" || port == wildcard || portNumber(port) {
		return nil
	}
	return fmt.Errorf("the port must be 1-65535 or *; got %q", port)
}

// portNumber: 1-65535 written without a sign or leading zeros.
func portNumber(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port
}

// checkDatabase: empty, * or a name of 1-63 bytes without NUL and *.
func checkDatabase(db string) error {
	if db == "" || db == wildcard {
		return nil
	}
	if len(db) > maxDatabaseBytes || strings.ContainsAny(db, "*\x00") {
		return fmt.Errorf("the database must be a name of 1-%d bytes without * or NUL, or * alone", maxDatabaseBytes)
	}
	return nil
}

// String is the pattern as it was given.
func (p Pattern) String() string {
	s := p.host
	if p.bracketed() {
		s = "[" + p.host + "]"
	}
	if p.port != "" {
		s += ":" + p.port
	}
	if p.database != "" {
		s += "/" + p.database
	}
	return s
}

// bracketed: an IPv6 address followed by a port or a database.
func (p Pattern) bracketed() bool {
	return strings.Contains(p.host, ":") && !strings.HasPrefix(p.host, "/") && (p.port != "" || p.database != "")
}

// Match says whether t is a target of the pattern.
func (p Pattern) Match(t sdk.Target) bool {
	return sameHost(p.host, t.Host) && wild(p.port, strconv.Itoa(t.Port)) && wild(p.database, t.Database)
}

func wild(pattern, value string) bool {
	return pattern == "" || pattern == wildcard || pattern == value
}

// sameHost: a socket directory by its clean path, an address by its value, a
// name without regard to case.
func sameHost(pattern, host string) bool {
	if strings.HasPrefix(pattern, "/") {
		return sameSocket(pattern, host)
	}
	a, b := net.ParseIP(pattern), net.ParseIP(host)
	if a != nil || b != nil {
		return a != nil && b != nil && a.Equal(b)
	}
	return strings.EqualFold(pattern, host)
}

// sameSocket: host is the socket directory of the pattern.
func sameSocket(pattern, host string) bool {
	return strings.HasPrefix(host, "/") && filepath.Clean(pattern) == filepath.Clean(host)
}
