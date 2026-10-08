// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestValidNames(t *testing.T) {
	for _, name := range []string{"a", "db", "pg-prod", "a_b", "0x", "Z9", strings.Repeat("a", 64)} {
		if !hostsetup.ValidName(name) {
			t.Errorf("%q was refused", name)
		}
	}
}

func TestInvalidNames(t *testing.T) {
	for _, name := range []string{"", "-db", "_db", "db.pass", "../db", "d b", "a/b", "db\n", strings.Repeat("a", 65), "é"} {
		if hostsetup.ValidName(name) {
			t.Errorf("%q was accepted", name)
		}
	}
}

func TestNameRefusalNamesTheRule(t *testing.T) {
	f := hostsetup.CheckName("secret", "db.pass")
	if f == nil || f.Reason != "NAME_INVALID" || !strings.Contains(f.Detail, "1-64") {
		t.Fatalf("refusal = %+v", f)
	}
	if hostsetup.CheckName("secret", "db") != nil {
		t.Fatal("a good name was refused")
	}
}
