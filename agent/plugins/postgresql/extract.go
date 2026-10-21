// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"path"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sdk.Extractor = Plugin{}

// PlanExtract implements sdk.Extractor: a snapshot of a database is laid out as
// its dump file and the file of the newest global objects of it, a snapshot of
// global objects as that one file (Р60, Р62). The agent restores a snapshot
// of any other part as a tree. The commands that restore the database are
// told in the hints, not run.
func (Plugin) PlanExtract(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	s := req.Snapshot
	switch tagValue(s.Tags, partTag) {
	case partDatabase:
		return planDatabase(req)
	case partGlobals:
		return planGlobals(req)
	}
	return sdk.ExtractPlan{}, nil
}

func planDatabase(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	l, f := planLayout(req.Snapshots, req.Snapshot)
	if f != nil {
		return sdk.ExtractPlan{}, plain(f)
	}
	files := []sdk.ExtractFile{extractFile(l.dump)}
	h := hints{target: req.Target, database: l.database, version: l.version, dump: files[0].Name}
	if l.globals != nil {
		files = append(files, extractFile(*l.globals))
		h.globals = files[1].Name
	}
	return sdk.ExtractPlan{Files: files, Hints: h.lines()}, nil
}

func planGlobals(req sdk.ExtractRequest) (sdk.ExtractPlan, error) {
	s := req.Snapshot
	if _, f := databaseName(s); f != nil {
		return sdk.ExtractPlan{}, plain(f)
	}
	file, f := fileOf(s)
	if f != nil {
		return sdk.ExtractPlan{}, plain(f)
	}
	out := extractFile(file)
	h := hints{target: req.Target, version: tagValue(s.Tags, versionTag), globals: out.Name}
	return sdk.ExtractPlan{Files: []sdk.ExtractFile{out}, Hints: h.lines()}, nil
}

// plain is the words of the refusal without its reason and stage: the agent
// words an error of a plan itself.
func plain(f *sdk.RestoreError) error { return errors.New(f.Detail) }

// extractFile is the file of the snapshot as the agent lays it out: in the root
// of the target, under the name it has in the snapshot.
func extractFile(f snapshotFile) sdk.ExtractFile {
	return sdk.ExtractFile{SnapshotID: f.snapshotID, Path: f.path, Name: path.Base(f.path)}
}

// hints are what the operator is told to run after the files are laid out.
type hints struct {
	target   string
	database string
	version  string
	// dump and globals are the names of the files; empty for a file the plan
	// does not hold.
	dump, globals string
}

func (h hints) lines() []string {
	lines := []string{
		"The files are readable by root only (root:root, mode 0600); give them to the user that runs PostgreSQL (chown) before the commands below.",
		"The database is not restored: the agent runs no PostgreSQL program. You run these commands" + h.versionLine(),
	}
	switch {
	case h.globals != "":
		lines = append(lines, "Global objects first, as a superuser of the new cluster:", "  psql -X -d postgres -f "+quote(h.target+"/"+h.globals))
	case h.dump != "":
		lines = append(lines, "There are no global objects in this backup: roles and their rights are not restored, so pg_restore goes without owners.")
	}
	if h.dump == "" {
		return lines
	}
	restore := "pg_restore -d "
	if h.globals == "" {
		restore = "pg_restore --no-owner --no-acl -d "
	}
	return append(lines, "Then the database:", "  createdb "+quote(h.database), "  "+restore+quote(h.database)+" "+quote(h.target+"/"+h.dump))
}

// versionLine says which programs must not be older than pg_dump was.
func (h hints) versionLine() string {
	programs := "psql"
	switch {
	case h.dump != "" && h.globals != "":
		programs = "pg_restore and psql"
	case h.dump != "":
		programs = "pg_restore"
	}
	if h.version == "" {
		return "; " + programs + " must be not older than the version of pg_dump that made the backup:"
	}
	return "; " + programs + " must be not older than " + h.version + ":"
}

// quote is s as one word of a shell: in single quotes, and a single quote
// inside it is written as a quote, a backslash and two quotes (gofmt would turn
// the literal text of that into a typographic quote in a comment).
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
