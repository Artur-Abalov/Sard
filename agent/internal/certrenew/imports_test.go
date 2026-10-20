// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"os/exec"
	"strings"
	"testing"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

// The renewal knows what the server said only through its own errors
// (CallError); the protocol is the business of transport.CertClient. So it
// imports neither gRPC nor the transport, directly or through another package
// of the agent.
//
// gRPC is checked among the direct imports only: identity reaches the generated
// protobuf package through role, and that pulls the gRPC module into every
// dependency list of the agent.
func TestRenewalDoesNotDependOnGRPCOrTheTransport(t *testing.T) {
	for _, pkg := range goList(t, "-f", `{{join .Imports "\n"}}`, ".") {
		if strings.HasPrefix(pkg, "google.golang.org/grpc") {
			t.Errorf("certrenew imports %s", pkg)
		}
	}
	for _, pkg := range goList(t, "-deps", "-f", "{{.ImportPath}}", ".") {
		if strings.HasSuffix(pkg, "agent/internal/transport") {
			t.Errorf("certrenew depends on %s", pkg)
		}
	}
}
