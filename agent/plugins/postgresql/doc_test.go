// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pluginDoc(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "plugins", "postgresql.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(doc)
}

func wantAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("docs/plugins/postgresql.md does not contain %q", want)
		}
	}
}

// Scenario: Документация плагина объясняет роль и секрет.
func TestTheDocumentationExplainsTheRoleAndTheSecret(t *testing.T) {
	wantAll(t, pluginDoc(t),
		"CREATE ROLE backup LOGIN",         // the role with LOGIN
		"GRANT pg_read_all_data TO backup", // and the right to read
		"BYPASSRLS",                        // with row level security
		"lo_compat_privileges",             // and large objects, which pg_read_all_data does not cover
		"secrets:",                         // the secret in agent.yaml
		"agent.yaml",                       //
		"0600",                             // the file of the secret
		"владельца агента",                 // owned by the agent's user
		"перезапуск",                       // the agent is restarted
		"в конфиге источника не пишется", // never the password itself
		"`password_ref`",    // only the name of the secret
		"postgresql-client", // the package of Debian and Ubuntu
		"postgresql",        // and of RHEL and Fedora
		"pg_dump_path",      // or the path of the tool
	)
}

// Scenario: Документация плагина объясняет восстановление руками.
func TestTheDocumentationExplainsTheRestoreByHand(t *testing.T) {
	wantAll(t, pluginDoc(t),
		"postgresql.main_snapshot",   // find the snapshot of the globals
		"psql -X -d postgres",        // restore the globals as a superuser first
		"суперпользовател",           //
		"restic dump",                // then the dump of the database
		"createdb",                   // into a new database
		"pg_restore",                 //
		"already exists",             // roles that exist already are expected
		"postgresql.pg_dump_version", // pg_restore and psql are not older than that
		"не старше",                  //
		"не содержит ролей и глобальных объектов", // the dump of a database has no roles
		"--no-owner --no-acl",    // without the globals
		"include_globals",        //
		"без паролей",            // roles come without passwords
		"ALTER ROLE",             // set them by hand
		"PASSWORD",               //
		"globals_role_passwords", // the hashes in the repository
		"хэши паролей",           //
		"my%20db.dump",           // the rule of the file names
		"my%20db.globals.sql",    //
	)
}
