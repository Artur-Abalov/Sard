// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The fake restic of A8c-2 (docs/specs/agent/repo-restore.feature): restore
// writes the files of a snapshot into the target, ls lists them.

// snapFile is a file of a snapshot.
type snapFile struct{ path, data string }

// fakeRestore is how one repository answers restic restore and restic ls.
type fakeRestore struct {
	// files are the files of each snapshot, by full id.
	files map[string][]snapFile
	// hang: restore never ends until SIGTERM.
	hang bool
	// fatal is the line restore prints on stderr before it exits with 1,
	// writing nothing.
	fatal string
	// items makes restore report that many files it could not restore.
	items int
	// summary, if not nil, is what restic reports instead of what it wrote.
	summary *restic.RestoreSummary
	// before runs when restore starts, with the target.
	before func(target string)
	// writeNothing: restore reports success but writes no file.
	writeNothing bool
}

// restoreArgs are the arguments of a restore call, taken apart.
type restoreArgs struct {
	target, overwrite, sftpCommand, snapshot string
	include                                  []string
}

func parseRestoreArgs(args []string) restoreArgs {
	var a restoreArgs
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--target":
			a.target = args[i+1]
		case "--overwrite":
			a.overwrite = args[i+1]
		case "--include":
			a.include = append(a.include, args[i+1])
		case "-o":
			if v, ok := strings.CutPrefix(args[i+1], "sftp.command="); ok {
				a.sftpCommand = v
			}
		}
	}
	a.snapshot = args[len(args)-1]
	return a
}

func (a restoreArgs) includes(path string) bool {
	if len(a.include) == 0 {
		return true
	}
	return slices.ContainsFunc(a.include, func(p string) bool { return path == p || strings.HasPrefix(path, p+"/") })
}

func (f *fakeRestic) restore(ctx context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	rs := &r.restoreState
	args := parseRestoreArgs(cmd.Args)
	args.target = inheritedTarget(args.target, cmd.ExtraFiles)
	if rs.before != nil {
		rs.before(args.target)
	}
	if rs.hang { // SIGTERM may come before or while it waits: either way it is terminated
		return f.hang(ctx, cmd, r, "")
	}
	if ctx.Err() != nil { // SIGTERM came while it ran
		return -1, nil
	}
	if rs.fatal != "" {
		cmd.Stderr([]byte(rs.fatal))
		return 1, nil
	}
	if rs.items > 0 {
		for i := range rs.items {
			cmd.Stderr([]byte(fmt.Sprintf(`{"message_type":"error","error":{"message":"lchown: operation not permitted"},"during":"restore","item":"/srv/data/f%02d"}`, i)))
		}
		cmd.Stderr([]byte(fmt.Sprintf(`{"message_type":"exit_error","code":1,"message":"Fatal: There were %d errors"}`, rs.items)))
		return 1, nil
	}
	summary := rs.write(f, args)
	if rs.summary != nil {
		summary = *rs.summary
	}
	out, _ := json.Marshal(map[string]any{"message_type": "summary", "files_restored": summary.Files, "bytes_restored": summary.Bytes})
	cmd.Stdout(out)
	return 0, nil
}

// inheritedTarget is the target as this process can write to it: restic
// would write to /proc/self/fd/3/root with the directory it inherited as
// descriptor 3; the fake is the same process as the command, and the
// directory is a file of it.
func inheritedTarget(target string, inherited []*os.File) string {
	rest, ok := strings.CutPrefix(target, "/proc/self/fd/3/")
	if !ok || len(inherited) == 0 {
		return target
	}
	return fmt.Sprintf("/proc/self/fd/%d/%s", inherited[0].Fd(), rest)
}

// writable: the target is inside the temporary directory of the test or is
// a directory the command handed on.
func writable(target string) bool {
	return strings.HasPrefix(target, os.TempDir()) || strings.HasPrefix(target, "/proc/self/fd/")
}

// write puts the files of the snapshot into the target, as restic does, but
// only inside the temporary directory of the test: a target "/" is recorded
// and never written.
func (rs *fakeRestore) write(f *fakeRestic, a restoreArgs) restic.RestoreSummary {
	var summary restic.RestoreSummary
	if rs.writeNothing || !writable(a.target) {
		return summary
	}
	for _, file := range rs.files[a.snapshot] {
		if written, ok := f.put(a, file); ok {
			summary.Files++
			summary.Bytes += written
		}
	}
	return summary
}

// put writes one file of the snapshot unless the paths asked for leave it
// out or restic is told not to replace a file that is there.
func (f *fakeRestic) put(a restoreArgs, file snapFile) (written uint64, done bool) {
	dest := filepath.Join(a.target, file.path)
	if _, err := os.Lstat(dest); !a.includes(file.path) || (err == nil && a.overwrite == "never") {
		return 0, false
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(file.data), 0o640); err != nil {
		f.t.Fatal(err)
	}
	return uint64(len(file.data)), true
}

// ls lists the files of the snapshot under the path asked for.
func (f *fakeRestic) ls(_ context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	id, path := cmd.Args[len(cmd.Args)-2], cmd.Args[len(cmd.Args)-1]
	cmd.Stdout([]byte(`{"time":"2026-10-10T06:00:00Z","paths":["/srv/data"],"message_type":"snapshot","struct_type":"snapshot"}`))
	for _, file := range r.restoreState.files[id] {
		if file.path == path || strings.HasPrefix(file.path, path+"/") {
			node, _ := json.Marshal(map[string]string{"path": file.path, "type": "file", "message_type": "node", "struct_type": "node"})
			cmd.Stdout(node)
		}
	}
	return 0, nil
}
