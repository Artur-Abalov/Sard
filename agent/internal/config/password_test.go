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
