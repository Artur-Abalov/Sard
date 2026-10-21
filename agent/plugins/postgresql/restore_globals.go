// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"strings"
)

// globals is the file of the global objects of a snapshot (the output of
// pg_dumpall --globals-only) as the restore reads it (ВК26, РВ7): line by
// line, the way pg_dumpall writes it, one statement to a line.
type globals struct {
	lines []string
	// roles are the roles the file creates (CREATE ROLE), in file order.
	roles []string
	// tablespaces are the tablespaces it creates.
	tablespaces []string
	// meta is the first line (1-based) with a command of psql other than the
	// guards of pg_dumpall (\restrict, \unrestrict); 0: none.
	meta int
}

// parseGlobals reads the file.
func parseGlobals(content string) globals {
	g := globals{lines: strings.Split(strings.TrimSuffix(content, "\n"), "\n")}
	for i, line := range g.lines {
		if g.meta == 0 && isMetaCommand(line) {
			g.meta = i + 1
		}
		if name, ok := identAfter(line, "CREATE ROLE "); ok {
			g.roles = append(g.roles, name)
		}
		if name, ok := identAfter(line, "CREATE TABLESPACE "); ok {
			g.tablespaces = append(g.tablespaces, name)
		}
	}
	return g
}

// isMetaCommand: psql runs a backslash command anywhere outside quotes (`; \!
// id`, an indented `\!`, `\gexec`), and \! runs a shell command, which the
// file of a snapshot must not hold. The rule is fail-safe (ADR 0065 §6): a line
// with a backslash is refused, except the exact \restrict and \unrestrict
// lines that pg_dump of 2025 and later writes around the script. A backslash
// in a literal is refused too, with a clear message.
func isMetaCommand(line string) bool {
	if !strings.Contains(line, `\`) {
		return false
	}
	for _, guard := range []string{`\restrict `, `\unrestrict `} {
		if token, ok := strings.CutPrefix(line, guard); ok && isGuardToken(token) {
			return false
		}
	}
	return true
}

// isGuardToken: the key of a guard is letters and digits.
func isGuardToken(token string) bool {
	return token != "" && strings.Trim(token, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == ""
}

// identAfter reads the identifier that follows prefix at the start of the
// line: bare, or in double quotes with a quote doubled.
func identAfter(line, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok || rest == "" {
		return "", false
	}
	if rest[0] == '"' {
		return quotedIdent(rest[1:])
	}
	end := strings.IndexAny(rest, "; \t")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end], end > 0
}

// quotedIdent reads the rest of a quoted identifier, after its opening quote.
func quotedIdent(rest string) (string, bool) {
	var name strings.Builder
	for i := 0; i < len(rest); i++ {
		switch {
		case rest[i] != '"':
			name.WriteByte(rest[i])
		case i+1 < len(rest) && rest[i+1] == '"':
			name.WriteByte('"')
			i++
		default:
			return name.String(), true
		}
	}
	return "", false // an identifier that is not closed
}

// without returns the script without the statements about the role: CREATE
// ROLE and ALTER ROLE of the bootstrap superuser of the target, which exists
// there already (ВК26).
func (g globals) without(role string) string {
	var kept []string
	for _, line := range g.lines {
		if c, ok := identAfter(line, "CREATE ROLE "); ok && c == role {
			continue
		}
		if a, ok := identAfter(line, "ALTER ROLE "); ok && a == role {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n") + "\n"
}

// quoted lists names the way a message shows them.
func quoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(q, ", ")
}
