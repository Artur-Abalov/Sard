// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Kind is what a snapshot is laid out as (Р60): by its tags, no new tag.
type Kind int

const (
	// KindDirectory: the files of the snapshot under their original paths (Р61).
	KindDirectory Kind = iota
	// KindDatabase: the dump of a PostgreSQL database (Р62).
	KindDatabase
	// KindGlobals: the global objects of a PostgreSQL cluster (Р62).
	KindGlobals
)

// The tags the postgresql plugin writes (postgresql-plugin.feature, ПГ11, ПГ17e).
const (
	partTag      = "postgresql.part"
	databaseTag  = "postgresql.database"
	mainTag      = "postgresql.main_snapshot"
	versionTag   = "postgresql.pg_dump_version"
	serverTag    = "postgresql.server_version"
	clusterTag   = "postgresql.system_identifier"
	partDatabase = "database"
	partGlobals  = "globals"
)

// KindOf is the kind of the snapshot: postgresql.part=database is a
// database, postgresql.part=globals the global objects, anything else a
// directory (files, snapshots without tags sard.*, other plugins).
func KindOf(s Snapshot) Kind {
	part := tagValue(s.Tags, partTag)
	switch {
	case part == nil:
		return KindDirectory
	case *part == partDatabase:
		return KindDatabase
	case *part == partGlobals:
		return KindGlobals
	}
	return KindDirectory
}

// PGFile is a file of a PostgreSQL snapshot that the agent writes to the
// root of the target: the file of snapshot SnapshotID at Path, called Name.
type PGFile struct{ SnapshotID, Path, Name string }

// PGPlan is what repo restore lays out for a PostgreSQL snapshot.
type PGPlan struct {
	Files []PGFile
	// Database is the name of the database, decoded from %XX.
	Database string
	// Version is postgresql.pg_dump_version; empty without the tag.
	Version string
	// ServerVersion is postgresql.server_version, SystemIdentifier
	// postgresql.system_identifier (R2b); empty without the tag.
	ServerVersion, SystemIdentifier string
	// Dump and Globals say which of the two files the plan holds.
	Dump, Globals bool
}

// PlanPostgres plans the files of the snapshot, a database or global
// objects. A database comes with the newest snapshot of global objects tagged
// with its full id among list, if there is one (Р62). A snapshot that is not
// shaped as the agent writes it (one file in the root, a database name that
// decodes) is refused: the name of the file comes from the snapshot and is
// written to the host.
func PlanPostgres(list []Snapshot, s Snapshot) (PGPlan, *refusal.Failure) {
	plan := PGPlan{Dump: KindOf(s) == KindDatabase, Globals: KindOf(s) == KindGlobals}
	plan.Version = valueOf(s.Tags, versionTag)
	plan.ServerVersion = valueOf(s.Tags, serverTag)
	plan.SystemIdentifier = valueOf(s.Tags, clusterTag)
	db, f := databaseName(s)
	if f != nil {
		return plan, f
	}
	plan.Database = db
	file, f := fileOf(s)
	if f != nil {
		return plan, f
	}
	plan.Files = []PGFile{file}
	if plan.Dump {
		f = plan.addGlobals(list, s)
	}
	return plan, f
}

// addGlobals adds the file of the newest snapshot of global objects of the
// database snapshot, if it has one.
func (p *PGPlan) addGlobals(list []Snapshot, database Snapshot) *refusal.Failure {
	g, found := newestGlobals(list, database)
	if !found {
		return nil
	}
	file, f := fileOf(g)
	if f != nil {
		return f
	}
	p.Files, p.Globals = append(p.Files, file), true
	return nil
}

func newestGlobals(list []Snapshot, database Snapshot) (Snapshot, bool) {
	var found []Snapshot
	for _, c := range list {
		if main := tagValue(c.Tags, mainTag); KindOf(c) == KindGlobals && main != nil && *main == database.ID {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return Snapshot{}, false
	}
	return slices.MaxFunc(found, func(a, b Snapshot) int {
		return cmp.Or(a.Time.Compare(b.Time), strings.Compare(a.ID, b.ID))
	}), true
}

// valueOf is the value of the tag, "" without it.
func valueOf(tags []string, name string) string {
	if value := tagValue(tags, name); value != nil {
		return *value
	}
	return ""
}

func malformed(s Snapshot, format string, args ...any) *refusal.Failure {
	return refusal.Fail(refusal.ResticOutputUnexpected, "snapshot %s is tagged as PostgreSQL but %s; it cannot be restored as a directory, and nothing was written", s.ShortID, fmt.Sprintf(format, args...))
}

// databaseName is the value of postgresql.database, decoded from %XX.
func databaseName(s Snapshot) (string, *refusal.Failure) {
	tag := tagValue(s.Tags, databaseTag)
	if tag == nil {
		return "", malformed(s, "has no tag %s", databaseTag)
	}
	name, err := url.PathUnescape(*tag)
	if err != nil {
		return "", malformed(s, "its tag %s is not a name written with %%XX", databaseTag)
	}
	return name, nil
}

// fileOf is the one file of the snapshot, which lies in the root: /<name>.
func fileOf(s Snapshot) (PGFile, *refusal.Failure) {
	if len(s.Paths) != 1 {
		return PGFile{}, malformed(s, "holds %d paths, not one file", len(s.Paths))
	}
	name, ok := rootName(s.Paths[0])
	if !ok {
		return PGFile{}, malformed(s, "its path %q is not a file in the root", s.Paths[0])
	}
	return PGFile{SnapshotID: s.ID, Path: s.Paths[0], Name: name}, nil
}

// rootName is the name of a file in the root, /<name>: one component that is
// neither empty nor "." nor "..".
func rootName(path string) (string, bool) {
	name, rooted := strings.CutPrefix(path, "/")
	return name, rooted && !slices.Contains([]string{"", ".", ".."}, name) && !strings.ContainsAny(name, "/\x00")
}

// Dumper gives the content of a file of a snapshot; restic.CLI is one.
type Dumper interface {
	Dump(ctx context.Context, snapshotID, path string, w io.Writer) error
}

// Hints are the lines the summary prints after the files: the commands that
// restore the database, which the agent does not run (Р62), with the files
// in target.
func (p PGPlan) Hints(target string) []string {
	lines := []string{
		"The files are readable by root only (root:root, mode 0600); give them to the user that runs PostgreSQL (chown) before the commands below.",
		"The database is not restored: the agent runs no PostgreSQL program. You run these commands" + p.versionLine(),
	}
	if p.Globals {
		lines = append(lines, "Global objects first, as a superuser of the new cluster:", "  psql -X -d postgres -f "+quote(target+"/"+p.fileOf(false)))
	} else if p.Dump {
		lines = append(lines, "There are no global objects in this backup: roles and their rights are not restored, so pg_restore goes without owners.")
	}
	if !p.Dump {
		return lines
	}
	restore := "pg_restore -d "
	if !p.Globals {
		restore = "pg_restore --no-owner --no-acl -d "
	}
	return append(lines, "Then the database:", "  createdb "+quote(p.Database), "  "+restore+quote(p.Database)+" "+quote(target+"/"+p.fileOf(true)))
}

// versionLine says which programs must not be older than pg_dump was.
func (p PGPlan) versionLine() string {
	programs := "psql"
	switch {
	case p.Dump && p.Globals:
		programs = "pg_restore and psql"
	case p.Dump:
		programs = "pg_restore"
	}
	if p.Version == "" {
		return "; " + programs + " must be not older than the version of pg_dump that made the backup:"
	}
	return "; " + programs + " must be not older than " + p.Version + ":"
}

// fileOf is the name of the dump file (dump true) or of the global objects.
func (p PGPlan) fileOf(dump bool) string {
	switch {
	case len(p.Files) == 0:
		return ""
	case dump:
		return p.Files[0].Name
	}
	return p.Files[len(p.Files)-1].Name
}

// quote is s as one word of a shell: in single quotes, and a single quote
// inside it is written as a quote, a backslash and two quotes (gofmt would turn
// the literal text of that into a typographic quote in a comment).
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
