// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
)

// S11: secret.bound says whether the agent would give the secret to a plugin.

func TestABoundSecretIsOKWithItsBinding(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "secret.bound", "pg"); l.status != "OK" || l.message != "bound to postgresql at db:5432/app" {
		t.Errorf("%+v", l)
	}
}

func TestASecretOfTheMainConfigWithoutABindingIsAnErrorWithTheFileToWrite(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Remove(h.path("secret-bindings/pg.yaml")))
	lines, _, code := h.checkLines()
	l := line(t, lines, "secret.bound", "pg")
	if l.status != "ERROR" || !strings.Contains(l.message, "no binding ("+h.path("secret-bindings/pg.yaml")+")") ||
		!strings.Contains(l.hint, "write its binding into "+h.path("secret-bindings/pg.yaml")+" by hand") || code == exitOK {
		t.Errorf("%+v, code %d", l, code)
	}
}

func TestASecretOfAFragmentWithoutABindingIsAnErrorWithTheCommandThatBindsIt(t *testing.T) {
	h := fullHost(t)
	h.write(h.path("secrets/db"), "value", 0o600)
	h.writeFragment("secret-db.yaml", "secrets:\n  db: "+h.path("secrets/db")+"\n", 0o640)
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "secret.bound", "db"); l.status != "ERROR" || !strings.Contains(l.hint, "sudo sard-agent secret set db --plugin") {
		t.Errorf("%+v", l)
	}
}

func TestABindingOthersCanChangeIsAnError(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Chmod(h.path("secret-bindings/pg.yaml"), 0o666))
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "secret.bound", "pg"); l.status != "ERROR" || !strings.Contains(l.message, "writable by group or others") {
		t.Errorf("%+v", l)
	}
}

func TestWithoutAServiceUserTheBindingIsNotChecked(t *testing.T) {
	h := fullHost(t)
	h.people = h.people[2:] // root only
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "secret.bound", "pg"); l.status != "SKIP" || !strings.Contains(l.message, "service.user") {
		t.Errorf("%+v", l)
	}
}
