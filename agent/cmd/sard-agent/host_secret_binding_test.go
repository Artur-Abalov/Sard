// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
)

// S11: secret set binds the secret to a plugin and its targets in a file of
// root next to the main config; the agent gives the secret nowhere else.

func TestASecretIsBoundToItsPluginAndTargetsInAFileOfRoot(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	code, stdout, stderr := h.set("db", "SECRET-MARKER", "--plugin", "postgresql",
		"--target", "db.example.com:5432/app", "--target", "/var/run/postgresql:5432/*",
		"--auth", "scram-sha-256,md5", "--allow-tls", "require", "--ca-file", "/etc/sard/pg-ca.pem", "--allow-no-channel-binding")
	assertCode(t, code, exitOK)
	file := h.path("secret-bindings/db.yaml")
	h.assertRootsFile(file, 0o640)
	h.assertRootsFile(h.path("secret-bindings"), 0o750)
	want := secretbind.Binding{Plugin: "postgresql", Auth: []string{"scram-sha-256", "md5"}, AllowTLS: "require", CAFile: "/etc/sard/pg-ca.pem", AllowNoChannelBinding: true}
	assertBinding(t, h.fileContent(file), want, "db.example.com:5432/app", "/var/run/postgresql:5432/*")
	containsAll(t, stdout, "bound to:   postgresql at db.example.com:5432/app, /var/run/postgresql:5432/* (auth scram-sha-256,md5; tls require; CA /etc/sard/pg-ca.pem; no channel binding)", "binding:    "+file)
	h.assertValuesHidden(stdout, stderr, h.fileContent(file))
}

// assertRootsFile: the file is root's, of the group of the service, with mode.
func (h *setupHost) assertRootsFile(path string, mode os.FileMode) {
	h.t.Helper()
	if o, _ := h.fsys.ownerOf(path); o != (ownerRec{0, serviceUID}) {
		h.t.Fatalf("%s: owner %+v", path, o)
	}
	h.assertMode(path, mode)
}

// assertBinding: the file holds want, bound to targets.
func assertBinding(t *testing.T, body string, want secretbind.Binding, targets ...string) {
	t.Helper()
	got, err := secretbind.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	gotTargets := make([]string, len(got.Targets))
	for i, p := range got.Targets {
		gotTargets[i] = p.String()
	}
	got.Targets = nil
	if !reflect.DeepEqual(got, want) || !slices.Equal(gotTargets, targets) {
		t.Fatalf("binding %+v at %v, want %+v at %v", got, gotTargets, want, targets)
	}
}

// containsAll: s holds every one of wants.
func containsAll(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(s, want) {
			t.Errorf("%q lacks %q", s, want)
		}
	}
}

func TestASecretIsNotSetWithoutABinding(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"secret", "set", "db", "--stdin", "--config", "C"},
		{"secret", "set", "db", "--plugin", "postgresql", "--stdin", "--config", "C"},
		{"secret", "set", "db", "--target", "db:5432/app", "--stdin", "--config", "C"},
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		h.stdinIs("SECRET-MARKER")
		code, _, stderr := h.sudo(args...)
		assertCode(t, code, exitUsage)
		if !strings.Contains(stderr, "--plugin and at least one --target are required") {
			t.Errorf("%v: stderr %q", args, stderr)
		}
		h.assertHostUnchanged(before)
	}
}

func TestABindingThatIsNotRightIsRefusedBeforeAnythingChanges(t *testing.T) {
	t.Parallel()
	for flags, words := range map[string]string{
		"--plugin postgresql --target *.example.com":                     "host",
		"--plugin postgresql --target db:0/app":                          "port",
		"--plugin postgresql --target db --auth kerberos":                `auth "kerberos"`,
		"--plugin postgresql --target db --allow-tls prefer":             `allow_tls "prefer"`,
		"--plugin postgresql --target db --ca-file pg-ca.pem":            "absolute",
		"--plugin mysql --target db":                                     "PLUGIN_UNKNOWN",
		"--plugin postgresql --target db:5432/app --target ::1:5432/app": "brackets",
	} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.set("db", "SECRET-MARKER", strings.Fields(flags)...)
		if code == exitOK || !strings.Contains(stderr, words) {
			t.Errorf("%s: code %d, stderr %q", flags, code, stderr)
		}
		h.assertHostUnchanged(before)
		h.assertValuesHidden(stdout, stderr)
	}
}

func TestTheSameValueWithAnotherBindingRewritesOnlyTheBinding(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	code, stdout, _ := h.set("db", "SECRET-MARKER")
	if code != exitOK || !strings.Contains(stdout, "unchanged") {
		t.Fatalf("again: %d %q", code, stdout)
	}
	code, stdout, _ = h.set("db", "SECRET-MARKER", "--plugin", "postgresql", "--target", "db2.example.com")
	if code != exitOK || strings.Contains(stdout, "unchanged") || !strings.Contains(stdout, "postgresql at db2.example.com") {
		t.Fatalf("rebound: %d %q", code, stdout)
	}
	if body := h.fileContent(h.path("secret-bindings/db.yaml")); !strings.Contains(body, "- db2.example.com") || strings.Contains(body, "db.example.com:5432/app") {
		t.Fatalf("binding %q", body)
	}
}

func TestSecretListJSONCarriesTheBindingOrItsAbsence(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER", "--plugin", "postgresql", "--target", "db.example.com:5432/app", "--allow-tls", "disable")
	h.write(h.path("secret-bindings/broken.yaml"), "plugin: [", 0o640)
	h.cfg.Secrets["broken"] = h.path("secrets/pg")
	h.saveConfig()
	code, stdout, stderr := h.sudo("secret", "list", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	rows := listedSecrets(t, stdout)
	want := map[string]secretJSONRow{
		"db":     {Name: "db", DefinedIn: h.path("agent.d/secret-db.yaml"), Binding: &bindingJSON{Plugin: "postgresql", Targets: []string{"db.example.com:5432/app"}, AllowTLS: "disable"}},
		"pg":     {Name: "pg", DefinedIn: h.cfgPath},
		"broken": {Name: "broken", DefinedIn: h.cfgPath, BindingError: rows["broken"].BindingError},
	}
	if !reflect.DeepEqual(rows, want) || !strings.Contains(stdout, `"binding":null`) {
		t.Errorf("rows %+v, want %+v", rows, want)
	}
	containsAll(t, rows["broken"].BindingError, "yaml")
	h.assertValuesHidden(stdout, stderr)
}

// listedSecrets are the rows of secret list --json by name.
func listedSecrets(t *testing.T, stdout string) map[string]secretJSONRow {
	t.Helper()
	var out struct{ Secrets []secretJSONRow }
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	rows := map[string]secretJSONRow{}
	for _, r := range out.Secrets {
		rows[r.Name] = r
	}
	return rows
}

func TestSecretListSaysWhereEachSecretIsBound(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	code, stdout, stderr := h.sudo("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
	fragment := h.path("agent.d/secret-db.yaml")
	w := max(len(fragment), len(h.cfgPath)) + 2
	want := fmt.Sprintf("NAME  %-*sBOUND_TO\ndb    %-*spostgresql at db.example.com:5432/app\npg    %-*snothing (no binding: given to no plugin)\n",
		w, "DEFINED_IN", w, fragment, w, h.cfgPath)
	if stdout != want {
		t.Fatalf("stdout\n%s\nwant\n%s", stdout, want)
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestABindingThatCannotBeWrittenIsAWriteErrorAndTheSecretIsNotDefined(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"mkdir", "rename"} {
		h := newSetupHost(t)
		h.fsys.failOn, h.fsys.failPath = op, "secret-bindings"
		code, _, stderr := h.set("db", "SECRET-MARKER")
		assertRefusal(t, code, stderr, exitWrite, "CONFIG_WRITE")
		h.assertAbsent(h.path("agent.d/secret-db.yaml"))
	}
}

func TestANewBindingOfTheSameValueLeavesTheFragmentAlone(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	h.set("db", "SECRET-MARKER")
	h.fsys.failOn, h.fsys.failPath = "rename", "agent.d"
	code, stdout, stderr := h.set("db", "SECRET-MARKER", "--plugin", "postgresql", "--target", "db2.example.com", "--auth", "md5")
	if code != exitOK {
		t.Fatalf("the fragment was written again: %d %s", code, stderr)
	}
	containsAll(t, stdout, "bound to:   postgresql at db2.example.com (auth md5)\n")
}

func TestSecretListSaysWhyABindingCannotBeUsedWithoutItsPath(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	if err := os.MkdirAll(h.path("secret-bindings/pg.yaml"), 0o750); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := h.sudo("secret", "list", "--config", "C")
	assertCode(t, code, exitOK)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "unusable: is a directory") || strings.Contains(lines[1], "secret-bindings") {
		t.Fatalf("stdout %q", stdout)
	}
}
