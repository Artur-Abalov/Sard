// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secretbind_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func target(host string, port int, db string) sdk.Target {
	return sdk.Target{Host: host, Port: port, Database: db}
}

func TestAPatternMatchesItsOwnTargetAndNoOther(t *testing.T) {
	cases := []struct {
		pattern string
		match   []sdk.Target
		miss    []sdk.Target
	}{
		{"db.example.com:5432/app",
			[]sdk.Target{target("db.example.com", 5432, "app"), target("DB.Example.COM", 5432, "app")},
			[]sdk.Target{target("db.example.com", 5433, "app"), target("db.example.com", 5432, "App"), target("evil.example", 5432, "app"), target("db.example.com.evil", 5432, "app"), target("10.0.0.5", 5432, "app")}},
		{"db.example.com:*/app",
			[]sdk.Target{target("db.example.com", 5432, "app"), target("db.example.com", 6432, "app")},
			[]sdk.Target{target("db.example.com", 5432, "other")}},
		{"db.example.com:5432/*",
			[]sdk.Target{target("db.example.com", 5432, "app"), target("db.example.com", 5432, "restored")},
			[]sdk.Target{target("db.example.com", 6432, "app")}},
		{"db.example.com",
			[]sdk.Target{target("db.example.com", 5432, "app"), target("db.example.com", 1, "x")},
			[]sdk.Target{target("other.example.com", 5432, "app")}},
		{"db.example.com/app",
			[]sdk.Target{target("db.example.com", 6432, "app")},
			[]sdk.Target{target("db.example.com", 6432, "other")}},
		{"[::1]:5432/app",
			[]sdk.Target{target("::1", 5432, "app"), target("0:0::1", 5432, "app")},
			[]sdk.Target{target("::2", 5432, "app"), target("localhost", 5432, "app")}},
		{"::1",
			[]sdk.Target{target("::1", 5432, "app")},
			[]sdk.Target{target("::2", 5432, "app")}},
		{"10.0.0.5:5432",
			[]sdk.Target{target("10.0.0.5", 5432, "app")},
			[]sdk.Target{target("10.0.0.50", 5432, "app"), target("db.example.com", 5432, "app")}},
		{"/var/run/postgresql:5432/app",
			[]sdk.Target{target("/var/run/postgresql", 5432, "app"), target("/var/run/postgresql/", 5432, "app")},
			[]sdk.Target{target("/var/run/postgresql", 5433, "app"), target("/tmp", 5432, "app"), target("/VAR/run/postgresql", 5432, "app")}},
		{"/var/run/postgresql",
			[]sdk.Target{target("/var/run/postgresql", 5433, "x")},
			[]sdk.Target{target("/var/run", 5433, "x")}},
		{"/var/run/postgresql:*/*",
			[]sdk.Target{target("/var/run/postgresql", 5433, "x")},
			nil},
	}
	for _, c := range cases {
		p, err := secretbind.ParsePattern(c.pattern)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", c.pattern, err)
		}
		for _, tg := range c.match {
			if !p.Match(tg) {
				t.Errorf("%q must match %s", c.pattern, tg)
			}
		}
		for _, tg := range c.miss {
			if p.Match(tg) {
				t.Errorf("%q must not match %s", c.pattern, tg)
			}
		}
	}
}

func TestAPatternIsWrittenBackAsItWasGiven(t *testing.T) {
	for _, s := range []string{"db.example.com:5432/app", "db:*/*", "[::1]:5432/app", "::1", "/var/run/postgresql:5432/app", "db", "db/app", "/s"} {
		p, err := secretbind.ParsePattern(s)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", s, err)
		}
		if p.String() != s {
			t.Errorf("String() = %q, want %q", p.String(), s)
		}
	}
}

func TestAPatternThatIsNotATargetIsRefusedWithTheReason(t *testing.T) {
	cases := map[string]string{
		"":                                   "empty",
		"*":                                  "host",
		"db*:5432/app":                       "host",
		"*.example.com:5432":                 "host",
		"db.example.com:0/app":               "port",
		"db.example.com:65536":               "port",
		"db.example.com:x":                   "port",
		"db.example.com:":                    "port",
		"db.example.com:5432/":               "database",
		"db.example.com:5432/a*":             "database",
		"::1:5432/app":                       "brackets",
		"[::1":                               "brackets",
		"[::1]x":                             "brackets",
		"[db.example.com]:5432":              "IP",
		"db,other:5432":                      "host",
		"user@db:5432":                       "host",
		"db host:5432":                       "host",
		"/var/run/pg:5432x":                  "port",
		"db:5432/" + strings.Repeat("d", 64): "database",
		strings.Repeat("h", 256):             "host",
		"db:5432/a\x00b":                     "database",
	}
	for s, want := range cases {
		_, err := secretbind.ParsePattern(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParsePattern(%q) = %v, want an error about %q", s, err, want)
		}
	}
}
