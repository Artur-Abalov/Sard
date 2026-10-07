// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// lookup is a host with the given users.
func lookup(users ...hostsetup.User) hostsetup.LookupFunc {
	return func(name string) (hostsetup.User, error) {
		for _, u := range users {
			if u.Name == name {
				return u, nil
			}
		}
		return hostsetup.User{}, hostsetup.ErrNoUser
	}
}

var serviceUser = hostsetup.User{Name: "sard-agent", UID: 990, GID: 990}

func request(euid uint32, mutating bool, users ...hostsetup.User) hostsetup.Request {
	return hostsetup.Request{
		EUID: euid, ServiceUser: "sard-agent", Lookup: lookup(users...),
		Mutating: mutating, NeedsUser: true, Command: "secret set db --stdin",
	}
}

func TestRootMayRunAnyCommandAndGetsTheServiceUser(t *testing.T) {
	for _, mutating := range []bool{true, false} {
		p, f := hostsetup.Authorize(request(0, mutating, serviceUser))
		if f != nil || p.Role != hostsetup.RoleRoot || p.Service != serviceUser {
			t.Fatalf("mutating=%v: principal %+v, refusal %v", mutating, p, f)
		}
	}
}

func TestRootWithoutAServiceUserOnTheHostIsRefused(t *testing.T) {
	_, f := hostsetup.Authorize(request(0, true))
	if f == nil || f.Reason != repoinit.ServiceUserUnknown || !strings.Contains(f.Detail, "sard-agent") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestRootDoesNotNeedAServiceUserForACommandThatNeedsNoUser(t *testing.T) {
	req := request(0, false)
	req.NeedsUser = false
	p, f := hostsetup.Authorize(req)
	if f != nil || p.Role != hostsetup.RoleRoot {
		t.Fatalf("principal %+v, refusal %v", p, f)
	}
}

func TestAServiceUserLookupThatFailsOtherwiseIsReportedAsUnknown(t *testing.T) {
	req := request(0, true)
	req.Lookup = func(string) (hostsetup.User, error) { return hostsetup.User{}, errors.New("nss broke") }
	_, f := hostsetup.Authorize(req)
	if f == nil || f.Reason != repoinit.ServiceUserUnknown || !strings.Contains(f.Detail, "nss broke") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestServiceUserMayReadButNotChange(t *testing.T) {
	p, f := hostsetup.Authorize(request(990, false, serviceUser))
	if f != nil || p.Role != hostsetup.RoleService || p.Service != serviceUser {
		t.Fatalf("read: principal %+v, refusal %v", p, f)
	}
	_, f = hostsetup.Authorize(request(990, true, serviceUser))
	if f == nil || f.Reason != repoinit.PrivilegesRequired {
		t.Fatalf("change: refusal = %+v", f)
	}
	for _, want := range []string{"run it with sudo", "sudo sard-agent secret set db --stdin"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q lacks %q", f.Detail, want)
		}
	}
}

func TestAnyOtherUserIsRefusedWithTheSudoHint(t *testing.T) {
	for _, mutating := range []bool{true, false} {
		_, f := hostsetup.Authorize(request(1000, mutating, serviceUser))
		if f == nil || f.Reason != repoinit.PrivilegesRequired || !strings.Contains(f.Detail, "sudo sard-agent secret set db --stdin") {
			t.Fatalf("mutating=%v: refusal = %+v", mutating, f)
		}
	}
}

func TestWhenTheServiceUserDoesNotExistNobodyButRootMayRun(t *testing.T) {
	_, f := hostsetup.Authorize(request(990, false))
	if f == nil || f.Reason != repoinit.PrivilegesRequired {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestTheHintLeavesOutWhatTheCallerKeepsSecret(t *testing.T) {
	req := request(1000, true, serviceUser)
	req.Command = "enroll"
	_, f := hostsetup.Authorize(req)
	if f == nil || !strings.Contains(f.Detail, "sudo sard-agent enroll") || strings.Contains(f.Detail, "sudo -u") {
		t.Fatalf("refusal = %+v", f)
	}
}

func TestEnrollStyleCommandRunsAsRootOrServiceUser(t *testing.T) {
	// Mutating false and NeedsUser true: the shape of enroll and repo init.
	if _, f := hostsetup.Authorize(request(990, false, serviceUser)); f != nil {
		t.Fatalf("service user refused: %v", f)
	}
	if _, f := hostsetup.Authorize(request(0, false, serviceUser)); f != nil {
		t.Fatalf("root refused: %v", f)
	}
}
