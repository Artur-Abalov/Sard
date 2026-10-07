// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// testServiceUID is the uid of the "service user" of the enroll tests: not
// root, so the command runs as the service user, as A2b's scenarios did
// before A8a (docs/specs/agent/agent-enroll.feature, the amendment of В19).
const testServiceUID = 4242

func testEnrollDeps(hostname hostnameFunc) enrollDeps {
	return enrollDeps{
		hostname: hostname, clock: realEnrollClock{}, dial: enroll.RealDial,
		euid:       testServiceUID,
		lookupUser: users(hostsetup.User{Name: "sard-agent", UID: testServiceUID, GID: testServiceUID}),
		fs:         hostsetup.OS{},
	}
}
