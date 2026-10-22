// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The test helpers stay out of the binary: nothing the agent links in is a
// test package or a package that exists for tests.
func TestTheBinaryLinksNoTestCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "testing" || strings.HasSuffix(pkg, "/certtest") || strings.HasSuffix(pkg, "/testplugin") {
			t.Errorf("the agent depends on %s", pkg)
		}
	}
}

// ADR 0027: the agent links only the Go stdlib, gRPC with its status types,
// protobuf, a YAML parser, a JSON Schema validator and Sard's own modules
// (golang.org/x/{net,sys,text} come with gRPC). A new module is a decision
// for an ADR and docs/dependencies.md, not for a change of this list alone.
var allowedModules = map[string]bool{
	"github.com/Artur-Abalov/sard/agent":             true,
	"github.com/Artur-Abalov/sard/agent/plugins/sdk": true,
	"github.com/Artur-Abalov/sard/proto/gen/go":      true,
	"google.golang.org/grpc":                         true,
	"google.golang.org/genproto/googleapis/rpc":      true,
	"google.golang.org/protobuf":                     true,
	"go.yaml.in/yaml/v3":                             true,
	"github.com/santhosh-tekuri/jsonschema/v6":       true,
	"golang.org/x/net":                               true,
	"golang.org/x/sys":                               true,
	"golang.org/x/text":                              true,
}

func TestTheBinaryLinksOnlyTheAllowedModules(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range strings.Fields(string(out)) {
		if !allowedModules[mod] {
			t.Errorf("the agent links module %s, which ADR 0027 does not allow", mod)
		}
	}
}
