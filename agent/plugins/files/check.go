// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
)

// maxNamedPaths is how many failed paths a message names.
const maxNamedPaths = 10

// failure is a listed path that cannot be backed up, and why.
type failure struct{ path, reason string }

func (f failure) String() string { return fmt.Sprintf("%q: %s", f.path, f.reason) }

func (p Plugin) fs() FS {
	if p.FS == nil {
		return osFS{}
	}
	return p.FS
}

// checkPaths looks at every listed path and returns one error naming the
// ones that fail (A6b Ф8). The file system may not answer, so the check runs
// beside ctx: a cancelled step does not wait for it.
func (p Plugin) checkPaths(ctx context.Context, paths []string) error {
	done := make(chan []failure, 1)
	go func() { done <- checkAll(ctx, p.fs(), paths) }()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case failures := <-done:
		return summarize(failures)
	}
}

func checkAll(ctx context.Context, fsys FS, paths []string) []failure {
	var out []failure
	for _, path := range paths {
		if ctx.Err() != nil {
			return out
		}
		if reason := check(fsys, filepath.Clean(path)); reason != "" {
			out = append(out, failure{path, reason})
		}
	}
	return out
}

// check returns why a path cannot be backed up, or "". A directory must open
// and list; a regular file must open; a symbolic link is refused, since
// restic would store the link and no data (A6b Ф9).
func check(fsys FS, path string) string {
	info, err := fsys.Lstat(path)
	if err != nil {
		return reason(err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return linkReason(fsys, path)
	case info.IsDir():
		return readable(fsys, path, true)
	case info.Mode().IsRegular():
		return readable(fsys, path, false)
	}
	return "" // a socket, a device: restic stores what there is
}

func linkReason(fsys FS, path string) string {
	target, err := fsys.Readlink(path)
	if err != nil {
		return "is a symbolic link; back up the path it points to"
	}
	return fmt.Sprintf("is a symbolic link to %q; back up the target path instead", target)
}

// readable opens the path and, for a directory, reads one name of its list.
func readable(fsys FS, path string, dir bool) string {
	f, err := fsys.Open(path)
	if err != nil {
		return reason(err)
	}
	defer func() { _ = f.Close() }()
	if dir {
		if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
			return reason(err)
		}
	}
	return ""
}

// reason is the operating system's answer without the path in it.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return "no such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return err.Error()
}

// summarize turns the failures into one line: how many, and the first ten.
func summarize(failures []failure) error {
	if len(failures) == 0 {
		return nil
	}
	head := fmt.Sprintf("paths that cannot be backed up (%d)", len(failures))
	if len(failures) > maxNamedPaths {
		head += fmt.Sprintf(", first %d", maxNamedPaths)
		failures = failures[:maxNamedPaths]
	}
	parts := make([]string, len(failures))
	for i, f := range failures {
		parts[i] = f.String()
	}
	return errors.New(head + ": " + strings.Join(parts, "; "))
}
