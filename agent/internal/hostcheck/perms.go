// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"io/fs"
	"strings"
)

// CheckPermissions is the identifier of the check of the permissions of the
// files of the agent (Ч11).
const CheckPermissions = "files.permissions"

// ServiceUser is the user the service runs as.
type ServiceUser struct {
	Name string
	UID  uint32
}

// FileFacts is what stat says about a file or a directory.
type FileFacts struct {
	Mode     fs.FileMode
	UID, GID uint32
}

// PermKind says which rule a file is judged by (Ч11).
type PermKind int

// The kinds: a secret file of A1 and a directory of secrets are the
// service's own (an ERROR when they are not), the config, agent.d and a
// fragment are root's (a WARN when others can change them).
const (
	PermSecret PermKind = iota + 1
	PermSecretDir
	PermConfig
	PermFragment
)

// PermFile is one file or directory and what stat said about it.
type PermFile struct {
	Path  string
	Kind  PermKind
	Facts FileFacts
}

const groupOtherBits fs.FileMode = 0o077

// problem is one violation: what is wrong, how serious, and the command.
type problem struct {
	status Status
	text   string
	fix    string
}

// PermissionsLine is the one line of files.permissions: every violation
// named with the command that removes it, the status the worst of them.
func PermissionsLine(svc ServiceUser, files []PermFile) Line {
	var problems []problem
	for _, f := range files {
		problems = append(problems, judgePerm(svc, f)...)
	}
	line := Line{Check: CheckPermissions, Status: OK, Message: "the files of the agent belong to the users and have the permissions the service needs"}
	if len(problems) == 0 {
		return line
	}
	var texts, fixes []string
	line.Status = Warn
	for _, p := range problems {
		texts = append(texts, p.text)
		fixes = append(fixes, p.fix)
		if p.status == Error {
			line.Status = Error
		}
	}
	line.Message = strings.Join(texts, "; ")
	line.Hint = strings.Join(fixes, "; ")
	return line
}

func judgePerm(svc ServiceUser, f PermFile) []problem {
	switch f.Kind {
	case PermSecret:
		return ownProblems(svc, f, 0o600)
	case PermSecretDir:
		return ownProblems(svc, f, 0o700)
	}
	return rootProblems(f)
}

// ownProblems: the file belongs to the service user and nobody else has a
// permission on it, or the service will not start (A1).
func ownProblems(svc ServiceUser, f PermFile, want fs.FileMode) []problem {
	var ps []problem
	if f.Facts.UID != svc.UID {
		ps = append(ps, problem{Error, fmt.Sprintf("%s is owned by uid %d, not by %s", f.Path, f.Facts.UID, svc.Name), fmt.Sprintf("sudo chown %s %s", svc.Name, f.Path)})
	}
	if mode := f.Facts.Mode.Perm(); mode&groupOtherBits != 0 {
		ps = append(ps, problem{Error, fmt.Sprintf("%s has mode %04o: group and others must have no permission", f.Path, mode), fmt.Sprintf("sudo chmod %04o %s", want, f.Path)})
	}
	return ps
}

// rootProblems: the settings can be changed by root only (ADR 0050).
func rootProblems(f PermFile) []problem {
	var ps []problem
	mode := f.Facts.Mode.Perm()
	if f.Facts.UID != 0 && mode&0o200 != 0 {
		ps = append(ps, problem{Warn, fmt.Sprintf("%s is owned by uid %d and that user can change it", f.Path, f.Facts.UID), "sudo chown root " + f.Path})
	}
	if mode&0o022 != 0 {
		fix := "sudo chmod g-w,o-w " + f.Path
		if f.Kind == PermFragment {
			fix = "sudo chmod 0640 " + f.Path
		}
		ps = append(ps, problem{Warn, fmt.Sprintf("%s has mode %04o: it can be changed by users other than root", f.Path, mode), fix})
	}
	return ps
}

// CanRead says whether the user uid, gid (and no other group) may read the
// file, or list and search the directory, going by the permission bits; it
// is how a command run by root sees the files as the service does.
func CanRead(f FileFacts, isDir bool, uid, gid uint32) bool {
	want := fs.FileMode(0o4)
	if isDir {
		want = 0o5
	}
	perm := f.Mode.Perm()
	switch {
	case f.UID == uid:
		return perm>>6&want == want
	case f.GID == gid:
		return perm>>3&want == want
	}
	return perm&want == want
}
