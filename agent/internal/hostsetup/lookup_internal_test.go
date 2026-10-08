// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"os/user"
	"testing"
)

func fakeLookup(uid, gid string) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		return &user.User{Username: name, Uid: uid, Gid: gid}, nil
	}
}

func TestLookupReadsDecimalIDsUpToTheLargestUint32(t *testing.T) {
	for _, c := range []struct {
		uid, gid string
		want     User
	}{
		{"990", "991", User{"svc", 990, 991}},
		{"99", "99", User{"svc", 99, 99}},
		{"3000000000", "4294967295", User{"svc", 3000000000, 4294967295}},
	} {
		got, err := lookupWith(fakeLookup(c.uid, c.gid), "svc")
		if err != nil || got != c.want {
			t.Errorf("uid %s gid %s: %+v, %v", c.uid, c.gid, got, err)
		}
	}
}

func TestLookupRefusesIDsThatAreNotDecimalUint32(t *testing.T) {
	for _, c := range [][2]string{{"a", "1"}, {"1", "a"}, {"4294967296", "1"}, {"1", "4294967296"}, {"-1", "1"}, {"", "1"}, {"0x10", "1"}} {
		if got, err := lookupWith(fakeLookup(c[0], c[1]), "svc"); err == nil {
			t.Errorf("uid %q gid %q accepted: %+v", c[0], c[1], got)
		}
	}
}

func TestLookupPassesOnWhatTheHostSaysOfAnUnknownUserAndOfOtherFailures(t *testing.T) {
	_, err := lookupWith(func(string) (*user.User, error) { return nil, user.UnknownUserError("x") }, "x")
	if !errors.Is(err, ErrNoUser) {
		t.Fatalf("unknown: %v", err)
	}
	boom := errors.New("nss broke")
	if _, err := lookupWith(func(string) (*user.User, error) { return nil, boom }, "x"); !errors.Is(err, boom) || errors.Is(err, ErrNoUser) {
		t.Fatalf("other failure: %v", err)
	}
}

func TestProcessUserFallsBackToTheUIDInDigits(t *testing.T) {
	name, uid := processUserWith(func(string) (*user.User, error) { return nil, errors.New("no passwd entry") }, 4321)
	if name != "4321" || uid != 4321 {
		t.Fatalf("%q %d", name, uid)
	}
	name, uid = processUserWith(func(id string) (*user.User, error) { return &user.User{Username: "u" + id}, nil }, 77)
	if name != "u77" || uid != 77 {
		t.Fatalf("%q %d", name, uid)
	}
}
