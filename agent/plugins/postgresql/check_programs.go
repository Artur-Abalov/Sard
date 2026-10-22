// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.ProgramChecker = Plugin{}

// clientTools are the programs of the PostgreSQL client in the order the check
// names them; the first is the anchor the others are looked for beside.
var clientTools = []string{"pg_dump", "psql", "pg_dumpall", "pg_restore"}

// programsHint is what to do about a missing or incomplete client.
const programsHint = "install the PostgreSQL client package (postgresql-client on Debian and Ubuntu, postgresql on RHEL and Fedora); see docs/plugins/postgresql.md"

// foundTool is a program of the client: where it was found (empty for one that
// was not) and its version (empty when its answer names none).
type foundTool struct {
	name, path string
	version    version
}

// CheckPrograms implements sdk.ProgramChecker (Ч18, Ч31). It looks for the tools
// the way the plugin does: pg_dump in the PATH of the agent, the others beside
// it. A tool is looked for by running it as the service user: a directory the
// service cannot enter has no tools. Without pg_dump the answer is SKIP: whether
// the host has PostgreSQL sources is not known to the agent, so the missing
// client is not a fault of the host. An incomplete client is.
func (Plugin) CheckPrograms(h sdk.ProgramHost) sdk.ProgramsCheck {
	tools := locateClient(h)
	if tools[0].path == "" {
		return sdk.ProgramsCheck{Status: sdk.ProgramsSkip, Hint: programsHint,
			Message: "PostgreSQL client tools are not installed; PostgreSQL sources on this host will fail until pg_dump is in the PATH or pg_dump_path is set in the source"}
	}
	check := sdk.ProgramsCheck{Status: sdk.ProgramsOK, Message: describeTools(tools)}
	var notes []string
	for _, t := range tools {
		if t.path == "" {
			notes = append(notes, t.name+" is not beside pg_dump")
			check.Status = worseStatus(check.Status, statusOfMissing(t.name))
		}
	}
	if differentMajors(tools) {
		notes = append(notes, "the tools are of different major versions")
		check.Status = worseStatus(check.Status, sdk.ProgramsWarn)
	}
	if len(notes) > 0 {
		check.Message += ": " + strings.Join(notes, "; ")
		check.Hint = programsHint
	}
	return check
}

// locateClient finds pg_dump in the first directory of the PATH that has it and
// the other tools in that directory.
func locateClient(h sdk.ProgramHost) []foundTool {
	tools := make([]foundTool, len(clientTools))
	for _, dir := range h.Path() {
		if tools[0] = probeTool(h, dir, clientTools[0]); tools[0].path == "" {
			continue
		}
		for i := 1; i < len(tools); i++ {
			tools[i] = probeTool(h, dir, clientTools[i])
		}
		break
	}
	return tools
}

// probeTool runs dir/name --version; a tool that does not start is not there.
func probeTool(h sdk.ProgramHost, dir, name string) foundTool {
	path := filepath.Join(dir, name)
	out, err := h.Run(path, "--version")
	if err != nil {
		return foundTool{name: name}
	}
	v, _ := scanVersion(out)
	return foundTool{name: name, path: path, version: v}
}

// statusOfMissing: psql and pg_dumpall are used by the plugin, pg_restore
// only for a restore by hand (A8c).
func statusOfMissing(name string) sdk.ProgramsStatus {
	if name == "pg_restore" {
		return sdk.ProgramsWarn
	}
	return sdk.ProgramsError
}

// worseStatus is the worse of two statuses that say how well the host serves
// the programs: an error, then a warning, then the first.
func worseStatus(a, b sdk.ProgramsStatus) sdk.ProgramsStatus {
	if a == sdk.ProgramsError || b == sdk.ProgramsError {
		return sdk.ProgramsError
	}
	if b == sdk.ProgramsWarn {
		return sdk.ProgramsWarn
	}
	return a
}

// differentMajors: two of the tools found are of different major versions.
func differentMajors(tools []foundTool) bool {
	seen := map[int]bool{}
	for _, t := range tools {
		if t.version.text != "" { // a tool that is not there has no version
			seen[t.version.major] = true
		}
	}
	return len(seen) > 1
}

func describeTools(tools []foundTool) string {
	var parts []string
	for _, t := range tools {
		if t.path == "" {
			continue // not there: the notes of CheckPrograms name it
		}
		if t.version.text == "" {
			parts = append(parts, t.name+" "+t.path)
		} else {
			parts = append(parts, fmt.Sprintf("%s %s %s", t.name, t.path, t.version.text))
		}
	}
	return strings.Join(parts, ", ")
}
