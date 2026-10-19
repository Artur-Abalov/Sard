// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"regexp"
	"strings"
)

// Tool is a program of the PostgreSQL client: where it was found (empty for
// a program that was not) and its version.
type Tool struct{ Name, Path, Version string }

var clientVersion = regexp.MustCompile(`\(PostgreSQL\) (\d+\S*)`)

// PostgresVersion reads the version from the answer of `<tool> --version`;
// empty for an answer that is not one.
func PostgresVersion(output string) string {
	m := clientVersion.FindStringSubmatch(output)
	if m == nil {
		return ""
	}
	return m[1]
}

var leadingDigits = regexp.MustCompile(`^\d+`)

// major is the major version number: 16 of 16.4, 18 of 18beta1.
func major(version string) string { return leadingDigits.FindString(version) }

const postgresHint = "install the PostgreSQL client package (postgresql-client on Debian and Ubuntu, postgresql on RHEL and Fedora); see docs/plugins/postgresql.md"

// PostgresLine is plugin.programs of the postgresql plugin (Ч18, Ч31).
// Without pg_dump the line is SKIP: whether the host has PostgreSQL sources
// is not known to the agent, so the missing client is not a fault of the
// host. An incomplete client is.
func PostgresLine(pgDump, psql, pgDumpall, pgRestore Tool) Line {
	line := Line{Check: CheckPluginPrograms, Subject: "postgresql", Status: OK}
	if pgDump.Path == "" {
		line.Status = Skip
		line.Message = "PostgreSQL client tools are not installed; PostgreSQL sources on this host will fail until pg_dump is in the PATH or pg_dump_path is set in the source"
		line.Hint = postgresHint
		return line
	}
	tools := []Tool{pgDump, psql, pgDumpall, pgRestore}
	var notes []string
	for _, t := range tools {
		if t.Path == "" {
			notes = append(notes, t.Name+" is not beside pg_dump")
			line.Status = worse(line.Status, statusOfMissing(t.Name))
		}
	}
	if differs(tools) {
		notes = append(notes, "the tools are of different major versions")
		line.Status = worse(line.Status, Warn)
	}
	line.Message = describe(tools)
	if len(notes) > 0 {
		line.Message += ": " + strings.Join(notes, "; ")
		line.Hint = postgresHint
	}
	return line
}

// statusOfMissing: psql and pg_dumpall are used by the plugin, pg_restore
// only for a restore by hand (A8c).
func statusOfMissing(name string) Status {
	if name == "pg_restore" {
		return Warn
	}
	return Error
}

func worse(a, b Status) Status {
	if a == Error || b == Error {
		return Error
	}
	if a == Warn || b == Warn {
		return Warn
	}
	return a
}

// differs: two of the tools found are of different major versions.
func differs(tools []Tool) bool {
	seen := map[string]bool{}
	for _, t := range tools {
		if t.Path != "" && t.Version != "" {
			seen[major(t.Version)] = true
		}
	}
	return len(seen) > 1
}

func describe(tools []Tool) string {
	var parts []string
	for _, t := range tools {
		switch {
		case t.Path == "":
		case t.Version == "":
			parts = append(parts, t.Name+" "+t.Path)
		default:
			parts = append(parts, fmt.Sprintf("%s %s %s", t.Name, t.Path, t.Version))
		}
	}
	return strings.Join(parts, ", ")
}
