// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The SDK is Apache-2.0: it must not depend on any AGPL module of Sard.
// Allowed Sard dependency: the Apache-2.0 generated proto code.
func TestSDKDoesNotImportAGPLCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !strings.HasPrefix(pkg, "github.com/Artur-Abalov/sard/") {
			continue
		}
		if strings.HasPrefix(pkg, "github.com/Artur-Abalov/sard/agent/plugins/sdk") ||
			strings.HasPrefix(pkg, "github.com/Artur-Abalov/sard/proto/gen/go") {
			continue
		}
		t.Errorf("SDK depends on AGPL package %s", pkg)
	}
}
