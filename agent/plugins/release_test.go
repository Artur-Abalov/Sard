// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build !e2e

package plugins_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins"
)

// pluginDocs is where every plugin of a release build is described.
const pluginDocs = "../../docs/plugins"

// goList returns one line of `go list -f format` per package of pattern,
// built with tags.
func goList(t *testing.T, tags, format, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-tags", tags, "-f", format, pattern).CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

// topPlugin returns the top-level plugin package imp belongs to, or "".
func topPlugin(imp string) string {
	if !isConcretePlugin(imp) {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(imp, pluginsPkg+"/"), "/")
	return pluginsPkg + "/" + name
}

// linkedPlugins returns the plugin packages the registry links in a build
// with tags.
func linkedPlugins(t *testing.T, tags string) []string {
	var linked []string
	for _, imp := range goList(t, tags, `{{join .Imports " "}}`, pluginsPkg) {
		if p := topPlugin(imp); p != "" {
			linked = append(linked, p)
		}
	}
	return linked
}

// A plugin package lives in the tree only while a build registers it: the
// release build (Builtin) or the e2e stand (stand_e2e.go). A stub kept
// aside is dead code; a stub registered is a promise the agent cannot keep
// (ADR 0056).
func TestEveryPluginPackageIsRegisteredByABuild(t *testing.T) {
	linked := append(linkedPlugins(t, ""), linkedPlugins(t, "e2e")...)
	for _, pkg := range goList(t, "", "{{.ImportPath}}", pluginsPkg+"/...") {
		if p := topPlugin(pkg); p != "" && !slices.Contains(linked, p) {
			t.Errorf("%s is registered by no build: delete it or add it to Builtin", p)
		}
	}
}

// Every plugin a release agent announces to the server is a working source
// with its own page in docs/plugins: the README promises nothing more.
func TestEveryReleasePluginIsDocumented(t *testing.T) {
	for _, name := range plugins.Registry("1.2.3").Names() {
		if _, err := os.Stat(filepath.Join(pluginDocs, name+".md")); err != nil {
			t.Errorf("release plugin %s has no %s/%s.md: %v", name, pluginDocs, name, err)
		}
	}
}
