// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

func TestURLPasswordIsThePasswordInTheUserinfo(t *testing.T) {
	cases := map[string]string{
		"rest:http://qa:p%40ss@127.0.0.1:9/x": "p@ss",
		"rest:http://qa@host/x":               "",
		"s3:https://s3.example.com/b":         "",
		"/srv/backup/main":                    "",
		"sftp:backup@nas.example.com:/main":   "",
		"rest:%zz":                            "",
	}
	for url, want := range cases {
		if got := config.URLPassword(url); got != want {
			t.Errorf("URLPassword(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestRedactURLHidesThePasswordOfTheUserinfoAndNothingElse(t *testing.T) {
	cases := map[string]string{
		"rest:https://u:URL-MARKER@rest.example.com/extra": "rest:https://u:***@rest.example.com/extra",
		"rest:http://qa:p%40ss@127.0.0.1:9/x":              "rest:http://qa:***@127.0.0.1:9/x",
		"rest:http://qa@host/x":                            "rest:http://qa@host/x",
		"s3:https://s3.example.com/b":                      "s3:https://s3.example.com/b",
		"s3:https://u:p@s3.example.com":                    "s3:https://u:***@s3.example.com",
		"/srv/backup/main":                                 "/srv/backup/main",
		"sftp:backup@nas.example.com:/main":                "sftp:backup@nas.example.com:/main",
		"rest:https://a@b:pw@host/p@th":                    "rest:https://a@b:***@host/p@th",
		"rest:https://host/p:q@r":                          "rest:https://host/p:q@r",
		"rest:https://u:p@host/u:p@x":                      "rest:https://u:***@host/u:p@x",
		"rest:%zz":                                         "rest:%zz",
	}
	for url, want := range cases {
		if got := config.RedactURL(url); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", url, got, want)
		}
	}
}
