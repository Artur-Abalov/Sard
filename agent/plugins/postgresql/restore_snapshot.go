// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"cmp"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The tags of the snapshots of this plugin and the tag of the step (the host
// writes the plugin's own with the prefix "postgresql."; F1 ПГ11, ПГ17e).
const (
	sourceTag    = "sard.source"
	partTag      = "postgresql.part"
	databaseTag  = "postgresql.database"
	mainTag      = "postgresql." + sdk.MainSnapshotTag
	versionTag   = "postgresql.pg_dump_version"
	serverTag    = "postgresql.server_version"
	clusterTag   = "postgresql.system_identifier"
	partDatabase = "database"
	partGlobals  = "globals"
)

// tagValue is the value of the tag name=value, "" without the tag or with an
// empty value.
func tagValue(tags []string, name string) string {
	for _, t := range tags {
		if value, ok := strings.CutPrefix(t, name+"="); ok && value != "" {
			return value
		}
	}
	return ""
}

func shortID(id string) string { return id[:min(len(id), 8)] }

// snapshotFile is a file of a snapshot the restore reads out of restic.
type snapshotFile struct{ snapshotID, path string }

// layout is what a snapshot of a database is made of and what it says about
// where it came from (R2b ВК21).
type layout struct {
	dump snapshotFile
	// globals is the file of the newest snapshot of global objects of the
	// database; nil if it has none.
	globals *snapshotFile
	// database is the name of the database, decoded from %XX.
	database string
	// version is postgresql.pg_dump_version, serverVersion postgresql.server_version,
	// systemID postgresql.system_identifier; empty without the tag.
	version, serverVersion, systemID string
}

// ofSource: the snapshot carries the sard.source of the step.
func ofSource(s sdk.SnapshotInfo, source string) bool { return tagValue(s.Tags, sourceTag) == source }

// chooseSnapshot is the snapshot of the step among those of its source: the
// host gives all the snapshots of the repository, and the step names the source
// the snapshot must belong to.
func chooseSnapshot(list []sdk.SnapshotInfo, req sdk.RestoreRequest) (sdk.SnapshotInfo, []sdk.SnapshotInfo, *sdk.RestoreError) {
	mine := slices.DeleteFunc(slices.Clone(list), func(s sdk.SnapshotInfo) bool { return !ofSource(s, req.Source) })
	i := slices.IndexFunc(mine, func(s sdk.SnapshotInfo) bool { return s.ID == req.SnapshotID })
	if i < 0 {
		return sdk.SnapshotInfo{}, nil, refuse(sdk.ReasonSnapshotUnknown, stageCheck,
			"no snapshot %s of source %s in repository %s; see the snapshots with `sard-agent repo snapshots %s`", req.SnapshotID, req.Source, req.Repository, req.Repository)
	}
	return mine[i], mine, nil
}

// planLayout lays out the snapshot of a database. It comes with the newest
// snapshot of global objects tagged with its full id among list, if there is one
// (Р62). A snapshot that is not shaped as the plugin writes it (one file in the
// root, a database name that decodes) is refused: the names come from the
// snapshot.
func planLayout(list []sdk.SnapshotInfo, s sdk.SnapshotInfo) (layout, *sdk.RestoreError) {
	l := layout{version: tagValue(s.Tags, versionTag), serverVersion: tagValue(s.Tags, serverTag), systemID: tagValue(s.Tags, clusterTag)}
	db, f := databaseName(s)
	if f != nil {
		return l, f
	}
	l.database = db
	if l.dump, f = fileOf(s); f != nil {
		return l, f
	}
	g, found := newestGlobals(list, s)
	if !found {
		return l, nil
	}
	file, f := fileOf(g)
	if f != nil {
		return l, f
	}
	l.globals = &file
	return l, nil
}

func newestGlobals(list []sdk.SnapshotInfo, database sdk.SnapshotInfo) (sdk.SnapshotInfo, bool) {
	var found []sdk.SnapshotInfo
	for _, c := range list {
		if tagValue(c.Tags, partTag) == partGlobals && tagValue(c.Tags, mainTag) == database.ID {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return sdk.SnapshotInfo{}, false
	}
	return slices.MaxFunc(found, func(a, b sdk.SnapshotInfo) int {
		return cmp.Or(a.Time.Compare(b.Time), strings.Compare(a.ID, b.ID))
	}), true
}

func malformed(s sdk.SnapshotInfo, format string, args ...any) *sdk.RestoreError {
	return refuse(sdk.ReasonResticOutputUnexpected, stageCheck, "snapshot %s is tagged as PostgreSQL but %s; it cannot be restored as a directory, and nothing was written", shortID(s.ID), fmt.Sprintf(format, args...))
}

// databaseName is the value of postgresql.database, decoded from %XX.
func databaseName(s sdk.SnapshotInfo) (string, *sdk.RestoreError) {
	tag := tagValue(s.Tags, databaseTag)
	if tag == "" {
		return "", malformed(s, "has no tag %s", databaseTag)
	}
	name, err := url.PathUnescape(tag)
	if err != nil {
		return "", malformed(s, "its tag %s is not a name written with %%XX", databaseTag)
	}
	return name, nil
}

// fileOf is the one file of the snapshot, which lies in the root: /<name>.
func fileOf(s sdk.SnapshotInfo) (snapshotFile, *sdk.RestoreError) {
	if len(s.Paths) != 1 {
		return snapshotFile{}, malformed(s, "holds %d paths, not one file", len(s.Paths))
	}
	if !inRoot(s.Paths[0]) {
		return snapshotFile{}, malformed(s, "its path %q is not a file in the root", s.Paths[0])
	}
	return snapshotFile{snapshotID: s.ID, path: s.Paths[0]}, nil
}

// inRoot: the path is /<name>, one component that is neither empty nor "." nor "..".
func inRoot(path string) bool {
	name, rooted := strings.CutPrefix(path, "/")
	return rooted && !slices.Contains([]string{"", ".", ".."}, name) && !strings.ContainsAny(name, "/\x00")
}
