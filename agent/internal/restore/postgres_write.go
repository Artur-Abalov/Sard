// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

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
	if isWritten(h, w.name, w.info) && h.dir.Remove(w.name) == nil {
		return ""
	}
	return fmt.Sprintf("; %s/%s was not removed: it is not the file the agent wrote", h.Path, w.name)
}

// isWritten reports whether the name in the target leads to the file whose
// identity is info (Р87).
func isWritten(h *Held, name string, info fs.FileInfo) bool {
	cur, err := h.dir.Lstat(name)
	return err == nil && hostsetup.SameFile(cur, info)
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
		// Р87: no unlink by a name whose identity cannot be confirmed; the next
		// run refuses the target as not empty and names the leftover (Р88).
		return nil, refusal.Fail(refusal.TargetWrite, "cannot look at the file %s/%s: %v; it was not removed because its identity could not be confirmed", h.Path, tmp, err)
	}
	return &pending{h: h, out: out, info: info, remove: tmp}, nil
}

// fill gives the file its owner and mode and then its content. The file is
// not closed when that fails: abandon judges its name first, with it still
// open, and closes it after.
func (p *pending) fill(ctx context.Context, d Dumper, file PGFile, target repoinit.Target) *refusal.Failure {
	w := &countingWriter{w: p.out}
	f := prepare(p.out, p.h.Path, p.remove)
	if f == nil {
		err := d.Dump(ctx, file.SnapshotID, file.Path, w)
		f = outcome(ctx, err, w.err, p.h.Path, target)
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
	if !isWritten(h, name, p.info) {
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
		tmp := "." + name + tempMarker + random()
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
