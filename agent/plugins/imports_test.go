// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package plugins_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	module     = "github.com/Artur-Abalov/sard/agent"
	pluginsPkg = module + "/plugins"
	sdkPkg     = pluginsPkg + "/sdk"
	mainPkg    = module + "/cmd/sard-agent"
)

// isConcretePlugin reports whether pkg is a built-in plugin (or its
// subpackage), as opposed to the plugins registry or the SDK.
func isConcretePlugin(pkg string) bool {
	return strings.HasPrefix(pkg, pluginsPkg+"/") && pkg != sdkPkg && !strings.HasPrefix(pkg, sdkPkg+"/")
}

func isSDK(pkg string) bool { return pkg == sdkPkg || strings.HasPrefix(pkg, sdkPkg+"/") }

// leavesTheSDK reports whether plugin pkg imports something of the agent
// other than the SDK and its own subpackages.
func leavesTheSDK(pkg, imp string) bool {
	return isConcretePlugin(pkg) && !isSDK(imp) && !strings.HasPrefix(imp, pkg)
}

// violation says which rule the import of imp by pkg breaks, or "".
func violation(pkg, imp string) string {
	switch {
	case leavesTheSDK(pkg, imp):
		return "a plugin may import only " + sdkPkg
	case isConcretePlugin(imp) && pkg != pluginsPkg && !isConcretePlugin(pkg):
		return "only " + pluginsPkg + " may import a concrete plugin"
	case imp == pluginsPkg && pkg != mainPkg:
		return "only " + mainPkg + " may import " + pluginsPkg
	}
	return ""
}

// Built-in plugins are written against the public SDK only; the agent's
// internals reach them through the registry alone (ADR 0027).
func TestBuiltinPluginsDependOnTheSDKOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", module+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		for _, imp := range fields[1:] {
			if !strings.HasPrefix(imp, module+"/") {
				continue
			}
			if v := violation(fields[0], imp); v != "" {
				t.Errorf("%s imports %s: %s", fields[0], imp, v)
			}
		}
	}
}
