// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func TestATargetIsWrittenAsTheOperatorBindsIt(t *testing.T) {
	cases := map[string]sdk.Target{
		"db.example.com:5432/app":     {Host: "db.example.com", Port: 5432, Database: "app"},
		"[::1]:6432/app":              {Host: "::1", Port: 6432, Database: "app"},
		"/var/run/postgresql:5432/db": {Host: "/var/run/postgresql", Port: 5432, Database: "db"},
		"10.0.0.5:5432":               {Host: "10.0.0.5", Port: 5432},
		"token":                       {Host: "token"},
	}
	for want, target := range cases {
		if got := target.String(); got != want {
			t.Errorf("%#v.String() = %q, want %q", target, got, want)
		}
	}
}

func TestTheCommandOfABindingErrorTakesTheTargetAsOneWordOfTheShell(t *testing.T) {
	cases := map[string]sdk.Target{
		"db.example.com:5432/app":            {Host: "db.example.com", Port: 5432, Database: "app"},
		"/var/run/postgresql:5432/a_b-c.d@e": {Host: "/var/run/postgresql", Port: 5432, Database: "a_b-c.d@e"},
		`'x;reboot:5432/app'`:                {Host: "x;reboot", Port: 5432, Database: "app"},
		`'db:5432/$(id)'`:                    {Host: "db", Port: 5432, Database: "$(id)"},
		`'db:5432/it'\''s'`:                  {Host: "db", Port: 5432, Database: "it's"},
		`'db:5432/a b'`:                      {Host: "db", Port: 5432, Database: "a b"},
		`'[::1]:5432/app'`:                   {Host: "::1", Port: 5432, Database: "app"},
		`'db:5432/a*'`:                       {Host: "db", Port: 5432, Database: "a*"},
		`''`:                                 {},
	}
	for want, target := range cases {
		err := &sdk.BindingError{Name: "pg", Plugin: "postgresql", Target: target, Unbound: true}
		if !strings.HasSuffix(err.Error(), " --target "+want) {
			t.Errorf("%#v: %q, want the target %s", target, err.Error(), want)
		}
	}
}

func TestABindingErrorNamesTheSecretThePluginAndTheTargetAndTellsWhatToRun(t *testing.T) {
	target := sdk.Target{Host: "evil.example", Port: 5432, Database: "app"}
	cases := map[string]*sdk.BindingError{
		`secret "pg" is not allowed for postgresql at evil.example:5432/app; if that is intended, run on the agent host: sudo sard-agent secret set pg --plugin postgresql --target evil.example:5432/app`: {Name: "pg", Plugin: "postgresql", Target: target},
		`secret "pg" has no binding on this host and is not given to any plugin; run on the agent host: sudo sard-agent secret set pg --plugin postgresql --target evil.example:5432/app`:                  {Name: "pg", Plugin: "postgresql", Target: target, Unbound: true},
		`secret "pg" is bound to the plugin files, not postgresql; run on the agent host: sudo sard-agent secret set pg --plugin postgresql --target evil.example:5432/app`:                                {Name: "pg", Plugin: "postgresql", Target: target, BoundTo: "files"},
	}
	for want, err := range cases {
		if err.Error() != want {
			t.Errorf("Error() = %q\nwant      %q", err.Error(), want)
		}
		if !errors.Is(fmt.Errorf("prepare: %w", err), sdk.ErrSecretNotAllowed) {
			t.Errorf("%q must match ErrSecretNotAllowed", err)
		}
		if errors.Is(err, sdk.ErrUnknownSecret) {
			t.Errorf("%q is not an unknown secret", err)
		}
	}
}
