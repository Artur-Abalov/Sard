// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// User is a user of this host, as /etc/passwd and /etc/group know it.
type User struct {
	Name     string
	UID, GID uint32
}

// ErrNoUser is what a LookupFunc returns for a name the host does not have.
var ErrNoUser = errors.New("no such user")

// LookupFunc finds a user by name (os/user in production: no cgo, so
// users of LDAP and SSSD are not seen, ADR 0048).
type LookupFunc func(name string) (User, error)

// Role is how the command may act on the host.
type Role int

const (
	// RoleRoot: euid 0, any command; files are handed to the service user.
	RoleRoot Role = iota + 1
	// RoleService: the service user itself, commands that read.
	RoleService
)

// Principal is who runs the command.
type Principal struct {
	Role Role
	// Service is the service user; zero for root when the command needs none.
	Service User
}

// Request is what Authorize needs to know about the command (Р5).
type Request struct {
	// EUID is the effective user id of the process.
	EUID uint32
	// ServiceUser is the name of the service user (service.user).
	ServiceUser string
	Lookup      LookupFunc
	// Mutating commands change the host: only root may run them.
	Mutating bool
	// NeedsUser: the command hands files or restic to the service user, so
	// root needs it to exist.
	NeedsUser bool
	// Command is the command line after "sard-agent", repeated in the hint.
	Command string
}

// Authorize applies the privilege rule (Р5): root may do anything, the
// service user may read, anyone else may not run the command.
func Authorize(r Request) (Principal, *repoinit.Failure) {
	service, err := r.Lookup(r.ServiceUser)
	switch {
	case r.EUID == 0:
		return authorizeRoot(r, service, err)
	case err == nil && service.UID == r.EUID && !r.Mutating:
		return Principal{Role: RoleService, Service: service}, nil
	}
	return Principal{}, repoinit.Fail(repoinit.PrivilegesRequired, "this user may not run this command; run it with sudo: sudo sard-agent %s", r.Command)
}

func authorizeRoot(r Request, service User, err error) (Principal, *repoinit.Failure) {
	if err != nil && r.NeedsUser {
		return Principal{}, repoinit.Fail(repoinit.ServiceUserUnknown, "the service user %q cannot be found on this host (%v); create it, or set service.user in the agent config", r.ServiceUser, err)
	}
	if err != nil {
		service = User{}
	}
	return Principal{Role: RoleRoot, Service: service}, nil
}
