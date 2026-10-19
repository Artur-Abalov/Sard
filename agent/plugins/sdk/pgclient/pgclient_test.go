// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package pgclient_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk/pgclient"
)

func TestEnvDropsTheVariablesOfPostgresAndTheLocale(t *testing.T) {
	got := pgclient.Env([]string{"PATH=/bin", "PGHOST=evil", "PGPASSFILE=/x", "LC_ALL=ru", "LC_MESSAGES=ru", "PGPASSWORD=old", "HOME=/h", "LANG=ru"}, nil)
	want := []string{"PATH=/bin", "HOME=/h", "LANG=ru", "LC_MESSAGES=C"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnvAddsThePasswordOnlyWhenThereIsOne(t *testing.T) {
	if got := pgclient.Env(nil, []byte("s3cret")); !slices.Equal(got, []string{"LC_MESSAGES=C", "PGPASSWORD=s3cret"}) {
		t.Errorf("with a password: %q", got)
	}
	if got := pgclient.Env(nil, []byte{}); !slices.Equal(got, []string{"LC_MESSAGES=C", "PGPASSWORD="}) {
		t.Errorf("with an empty password: %q", got)
	}
}

func TestConninfoQuotesEveryValue(t *testing.T) {
	got := pgclient.Conninfo([][2]string{{"host", "db"}, {"dbname", "x host=evil"}, {"user", `a'b\c`}})
	want := `host='db' dbname='x host=evil' user='a\'b\\c'`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
