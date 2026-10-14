// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
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
	if value := tagValue(s.Tags, versionTag); value != nil {
		plan.Version = *value
	}
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

// maxTempNames is how many random names a temporary file is tried with.
const maxTempNames = 8

// written is a file the agent wrote, as it was when it was written: the
// identity (device and inode) that Р87 compares a name with.
type written struct {
	name string
	info fs.FileInfo
	// out stays open until the whole write is over, so that the inode of the
	// file cannot be given to another one (see pending.close).
	out hostsetup.File
}

// WritePostgres writes the files of the plan into the held target, each
// the way Р62 says: a temporary file .<name>.tmp-<random> made O_EXCL, given
// root:root and 0600 before any content (Н29, whatever the umask), filled by
// restic, synced, moved to its name without replacing, compared with the file
// written (Р87) and the directory synced. A failure leaves neither a temporary
// nor a final file: the files this run already finished are taken back. A
// name that leads to another file than the agent wrote is never removed; the
// failure names it. random is the random part of a name.
func WritePostgres(ctx context.Context, d Dumper, h *Held, plan PGPlan, random func() string, target repoinit.Target) *refusal.Failure {
	var done []written
	defer func() {
		for _, w := range done {
			_ = w.out.Close()
		}
	}()
	for _, file := range plan.Files {
		w, f := writeFile(ctx, d, h, file, random, target)
		if f != nil {
			for _, back := range done {
				f.Detail += takeBack(h, back)
			}
			return f
		}
		done = append(done, w)
	}
	return nil
}

// takeBack removes the name if it still leads to the file the agent wrote;
// otherwise it stays, and the text that follows a failure names it (Р87).
func takeBack(h *Held, w written) string {
	if cur, err := h.dir.Lstat(w.name); err == nil && hostsetup.SameFile(cur, w.info) && h.dir.Remove(w.name) == nil {
		return ""
	}
	return fmt.Sprintf("; %s/%s was not removed: it is not the file the agent wrote", h.Path, w.name)
}

// pending is a file being written: its temporary name, until it is moved,
// and then the final one, once that is judged to be the file written.
type pending struct {
	h      *Held
	out    hostsetup.File
	info   fs.FileInfo
	remove string // the name an abandoned file is taken back by
	closed bool
}

func writeFile(ctx context.Context, d Dumper, h *Held, file PGFile, random func() string, target repoinit.Target) (written, *refusal.Failure) {
	p, f := newPending(h, file.Name, random)
	if f != nil {
		return written{}, f
	}
	if f = p.fill(ctx, d, file, target); f == nil {
		f = p.commit(file.Name)
	}
	if f != nil {
		f.Detail += p.abandon()
		return written{}, f
	}
	p.closed = true // WritePostgres closes it at the end
	return written{name: file.Name, info: p.info, out: p.out}, nil
}

func newPending(h *Held, name string, random func() string) (*pending, *refusal.Failure) {
	tmp, out, f := createTemp(h, name, random)
	if f != nil {
		return nil, f
	}
	info, err := out.Stat()
	if err != nil {
		_ = out.Close()
		_ = h.dir.Remove(tmp)
		return nil, refusal.Fail(refusal.TargetWrite, "cannot look at the file %s/%s: %v", h.Path, tmp, err)
	}
	return &pending{h: h, out: out, info: info, remove: tmp}, nil
}

// fill gives the file its owner and mode and then its content; the file is
// closed when that fails.
func (p *pending) fill(ctx context.Context, d Dumper, file PGFile, target repoinit.Target) *refusal.Failure {
	w := &countingWriter{w: p.out}
	f := prepare(p.out, p.h.Path, p.remove)
	if f == nil {
		err := d.Dump(ctx, file.SnapshotID, file.Path, w)
		f = outcome(ctx, err, w.err, p.h.Path, target)
	}
	if f != nil {
		_ = p.close()
	}
	return f
}

// close closes the file once. The file stays open until its name is judged:
// while the agent holds it, its inode cannot be given to another file, so the
// comparison of Р87 cannot be fooled by a file that reuses the number.
func (p *pending) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	return p.out.Close()
}

// commit syncs the file, gives it its name, compares the name with the file
// written (Р87) and syncs the directory.
func (p *pending) commit(name string) *refusal.Failure {
	h := p.h
	if err := p.out.Sync(); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot save %s/%s: %v; no file of the snapshot was left in the target", h.Path, name, err)
	}
	if err := h.dir.RenameNoReplace(p.remove, h.dir, name); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot give the file its name %s/%s: %v; no file of the snapshot was left in the target", h.Path, name, err)
	}
	p.remove = ""
	if cur, err := h.dir.Lstat(name); err != nil || !hostsetup.SameFile(cur, p.info) {
		return refusal.Fail(refusal.TargetWrite, "%s/%s is not the file the agent wrote (someone else writes to the target) and was not removed; restore into a new target (the command makes it with owner root and mode 0700) or into a directory only root can write", h.Path, name)
	}
	p.remove = name
	if err := h.dir.Sync(); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot sync the target %s: %v; no file of the snapshot was left in it", h.Path, err)
	}
	return nil
}

// abandon takes back what the file left: the temporary file, or the final
// one if it was judged to be the file written. The text it returns names a
// name that was left because it is not that file.
func (p *pending) abandon() string {
	defer func() { _ = p.close() }()
	if p.remove == "" {
		return ""
	}
	return takeBack(p.h, written{name: p.remove, info: p.info})
}

// createTemp makes the temporary file of name, trying another random part
// for a name that is taken.
func createTemp(h *Held, name string, random func() string) (string, hostsetup.File, *refusal.Failure) {
	for range maxTempNames {
		tmp := "." + name + ".tmp-" + random()
		out, err := h.dir.CreateFile(tmp, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, refusal.Fail(refusal.TargetWrite, "cannot make a file in the target %s: %v", h.Path, err)
		}
		return tmp, out, nil
	}
	return "", nil, refusal.Fail(refusal.TargetWrite, "cannot find a free name for a temporary file in the target %s", h.Path)
}

// prepare gives the file its owner and mode through its descriptor, before
// content is written, so a umask does not matter and nobody reads it open.
func prepare(out hostsetup.File, dir, tmp string) *refusal.Failure {
	if err := errors.Join(out.Chown(rootOnly.UID, rootOnly.GID), out.Chmod(0o600)); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot set the owner and the mode of %s/%s: %v", dir, tmp, err)
	}
	return nil
}

// outcome judges the dump: a failure to write the file is the target's, any
// other failure is restic's; both say no file is left.
func outcome(ctx context.Context, dumpErr, writeErr error, dir string, target repoinit.Target) *refusal.Failure {
	switch {
	case writeErr != nil:
		return refusal.Fail(refusal.TargetWrite, "cannot write to the target %s: %v; no file of the snapshot was left in it", dir, writeErr)
	case dumpErr != nil:
		f := repoinit.FromRestic(ctx, dumpErr, target)
		f.Detail += "; there is no dump file in " + dir
		return f
	}
	return nil
}

// countingWriter remembers the first error of the file, so that it can be
// told from the error restic ends with when its output cannot be written.
type countingWriter struct {
	w   io.Writer
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
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
