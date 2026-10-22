// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The settings of a restore into a new database are read from the config by the
// plugin; the SDK knows no database (R2c-3).

const settingsConfig = `{"host": "db2", "database": "app_restored_20261011", "user": "restorer", "password_ref": "pg-restore", "tls_mode": "disable"`

func TestTheSettingsOfARestoreCarryTheConnectionAndTheChoices(t *testing.T) {
	got, err := parseRestore([]byte(settingsConfig + `, "port": 6432, "tls_root_cert": "/etc/ca.pem", "restore_globals": true, "pg_restore_path": "/opt/pg/bin/pg_restore"}`))
	want := restoreSettings{
		to:       target{Host: "db2", Port: 6432, User: "restorer", PasswordRef: "pg-restore", TLSMode: "disable", TLSRootCert: "/etc/ca.pem", ClientPath: "/opt/pg/bin/pg_restore"},
		database: "app_restored_20261011", globals: true,
	}
	if err != nil || got != want {
		t.Fatalf("settings = %+v, %v", got, err)
	}
}

func TestTheSettingsOfARestoreDefaultThePortAndLeaveGlobalsOff(t *testing.T) {
	got, err := parseRestore([]byte(settingsConfig + `}`))
	if err != nil || got.to.Port != 5432 || got.globals || got.to.ClientPath != "" {
		t.Fatalf("settings = %+v, %v", got, err)
	}
}

func TestTheNameOfTheNewDatabaseIsOneToSixtyThreeBytesWithoutNUL(t *testing.T) {
	for name, tt := range map[string]struct {
		database string
		ok       bool
	}{
		"one byte":         {"a", true},
		"63 bytes":         {strings.Repeat("a", 63), true},
		"64 bytes":         {strings.Repeat("a", 64), false},
		"two-byte letters": {strings.Repeat("ж", 32), false},
		"empty":            {"", false},
		"NUL":              {"a\x00b", false},
		"quotes":           {`x"; DROP DATABASE app; --`, true},
	} {
		_, err := parseRestore([]byte(`{"host": "h", "user": "u", "database": ` + jsonString(tt.database) + `}`))
		var cfgErr *sdk.ConfigError
		switch {
		case tt.ok && err != nil:
			t.Errorf("%s: %v", name, err)
		case !tt.ok && (!errors.As(err, &cfgErr) || cfgErr.Violations[0].Path != "/database"):
			t.Errorf("%s: err = %v, want a violation at /database", name, err)
		}
	}
}

func jsonString(s string) string {
	b := new(strings.Builder)
	b.WriteByte('"')
	for _, r := range s {
		if r < 0x20 || r == '"' || r == '\\' {
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&15])
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
