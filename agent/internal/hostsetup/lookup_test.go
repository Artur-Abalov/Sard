// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"os"
	"os/user"
	"strconv"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestLookupOSFindsTheCurrentUserWithUIDAndGID(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	got, err := hostsetup.LookupOS(cur.Username)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != cur.Username || int(got.UID) != os.Getuid() {
		t.Fatalf("user %+v, current %+v", got, cur)
	}
	gid, _ := strconv.Atoi(cur.Gid)
	if int(got.GID) != gid {
		t.Fatalf("gid %d, want %d", got.GID, gid)
	}
}

func TestLookupOSReportsAnUnknownUserAsErrNoUser(t *testing.T) {
	if _, err := hostsetup.LookupOS("no-such-user-sard-test"); !errors.Is(err, hostsetup.ErrNoUser) {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessUserNamesTheUserOfTheProcess(t *testing.T) {
	name, uid := hostsetup.ProcessUser()
	if int(uid) != os.Getuid() || name == "" {
		t.Fatalf("name %q uid %d", name, uid)
	}
}
