// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"strings"
)

// The fingerprint counts the tables pg_dump dumps: those that no
// --exclude-schema or --exclude-table pattern matches. A pattern is translated
// as psql and pg_dump translate it (patternToSQLRegex in fe_utils, without
// force_escape), and PostgreSQL itself matches the regular expressions, so
// that the two never disagree.

// patternParts splits a pattern at its unquoted dots into regular
// expressions, one per part: unquoted letters are folded to lower case, *
// and ? are wildcards, a dollar is always literal, inside double quotes the
// special characters are literal and "" is a quote.
func patternParts(pattern string) []string {
	var parts []string
	var cur strings.Builder
	inQuotes := false
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '"':
			var literal bool
			if inQuotes, literal = quoteMark(runes, i, inQuotes); literal {
				cur.WriteRune('"')
				i++
			}
		case !inQuotes && ch == '.':
			parts = append(parts, "^("+cur.String()+")$")
			cur.Reset()
		default:
			cur.WriteString(translate(ch, inQuotes))
		}
	}
	return append(parts, "^("+cur.String()+")$")
}

// quoteMark reads the double quote at i: "" inside quotes is a literal quote
// (true), any other opens or closes the quotes.
func quoteMark(runes []rune, i int, inQuotes bool) (bool, bool) {
	if inQuotes && i+1 < len(runes) && runes[i+1] == '"' {
		return true, true
	}
	return !inQuotes, false
}

// translate is one character of a part as a piece of the regular expression.
func translate(ch rune, inQuotes bool) string {
	if ch == '$' {
		return `\$`
	}
	if inQuotes {
		return inQuote(ch)
	}
	return outOfQuotes(ch)
}

// inQuote: inside double quotes a special character of a regular expression is literal.
func inQuote(ch rune) string {
	if strings.ContainsRune(`|*+?()[]{}.^\`, ch) {
		return `\` + string(ch)
	}
	return string(ch)
}

// outOfQuotes: letters fold to lower case, * and ? are wildcards, the rest passes
// as it is, as a piece of a regular expression.
func outOfQuotes(ch rune) string {
	switch {
	case ch >= 'A' && ch <= 'Z':
		return string(ch + 'a' - 'A')
	case ch == '*':
		return ".*"
	case ch == '?':
		return "."
	}
	return string(ch)
}

// emptyPart is the expression of a part with nothing in it: it constrains nothing.
const emptyPart = "^()$"

func regexMatch(column, re string) string {
	return fmt.Sprintf("%s OPERATOR(pg_catalog.~) %s COLLATE pg_catalog.default", column, literal(re))
}

// literal is a string constant of SQL, with standard_conforming_strings on.
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// tableCondition is the condition on pg_class c and pg_namespace n of an
// --exclude-table pattern: [database.][schema.]name; without a schema, the
// tables visible in the search path.
func tableCondition(pattern string) (string, error) {
	parts := patternParts(pattern)
	if len(parts) > 3 {
		return "", fmt.Errorf("exclude_tables pattern %q: improper qualified name (too many dotted names)", pattern)
	}
	var conds []string
	if name := parts[len(parts)-1]; name != emptyPart {
		conds = append(conds, regexMatch("c.relname", name))
	}
	if len(parts) >= 2 && parts[len(parts)-2] != emptyPart {
		conds = append(conds, regexMatch("n.nspname", parts[len(parts)-2]))
	} else {
		conds = append(conds, "pg_catalog.pg_table_is_visible(c.oid)")
	}
	return "(" + strings.Join(conds, " AND ") + ")", nil
}

// schemaCondition is the condition of an --exclude-schema pattern: [database.]schema.
func schemaCondition(pattern string) (string, error) {
	parts := patternParts(pattern)
	if len(parts) > 2 {
		return "", fmt.Errorf("exclude_schemas pattern %q: improper qualified name (too many dotted names)", pattern)
	}
	if schema := parts[len(parts)-1]; schema != emptyPart {
		return "(" + regexMatch("n.nspname", schema) + ")", nil
	}
	return "(true)", nil
}

// conditions are the conditions of the patterns, in order.
func conditions(patterns []string, condition func(string) (string, error)) ([]string, error) {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		cond, err := condition(p)
		if err != nil {
			return nil, err
		}
		out = append(out, cond)
	}
	return out, nil
}

// excludedCondition is true for a table that some pattern of the config
// leaves out of the dump.
func excludedCondition(c config) (string, error) {
	schemas, err := conditions(c.ExcludeSchemas, schemaCondition)
	if err != nil {
		return "", err
	}
	tables, err := conditions(c.ExcludeTables, tableCondition)
	if err != nil {
		return "", err
	}
	conds := append(schemas, tables...)
	if len(conds) == 0 {
		return "false", nil
	}
	return "(" + strings.Join(conds, " OR ") + ")", nil
}
