// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// LookupOS finds a user in /etc/passwd and /etc/group through os/user;
// built without cgo, it does not see users of LDAP or SSSD.
func LookupOS(name string) (User, error) {
	u, err := user.Lookup(name)
	if err != nil {
		var unknown user.UnknownUserError
		if errors.As(err, &unknown) {
			return User{}, ErrNoUser
		}
		return User{}, err
	}
	uid, uerr := strconv.ParseUint(u.Uid, 10, 32)
	gid, gerr := strconv.ParseUint(u.Gid, 10, 32)
	if uerr != nil || gerr != nil {
		return User{}, fmt.Errorf("user %s has a non-numeric id (uid %q, gid %q)", name, u.Uid, u.Gid)
	}
	return User{Name: name, UID: uint32(uid), GID: uint32(gid)}, nil
}

// ProcessUser is the name and the uid of the user of this process; the
// name is the uid in digits if the host does not know it.
func ProcessUser() (name string, uid uint32) {
	uid = uint32(os.Getuid())
	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err != nil {
		return strconv.Itoa(int(uid)), uid
	}
	return u.Username, uid
}
