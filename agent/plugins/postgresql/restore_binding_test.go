// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// S11 for a restore (R2): the secret is asked for every database the restore
// signs in to on the target server, the new one and the maintenance database
// postgres, and a refusal of either stops the restore before anything runs.

func TestARestoreAsksForTheSecretOfTheNewDatabaseAndOfTheMaintenanceDatabase(t *testing.T) {
	h := newPGHost(t)
	if _, f := h.run(h.request()); f != nil {
		t.Fatal(f)
	}
	want := []sdk.Target{{Host: "db2", Port: 5432, Database: rpNewDatabase}, {Host: "db2", Port: 5432, Database: "postgres"}}
	if !slices.Equal(h.host.asked, want) {
		t.Errorf("asked %v, want %v", h.host.asked, want)
	}
}

func TestARestoreWhoseBindingLeavesOutTheMaintenanceDatabaseFailsBeforeAnyProgramRuns(t *testing.T) {
	h := newPGHost(t)
	h.host.refuseAt = "postgres"
	_, f := h.run(h.request())
	hasPrefix(t, f, "SECRET_NOT_ALLOWED (check)")
	mentions(t, f.Error(), `secret "pg-restore" is not allowed for postgresql at db2:5432/postgres`)
	if len(h.server.ran("psql")) != 0 || len(h.repo.dumped()) != 0 {
		t.Error("something ran")
	}
}

func TestTheRollbackSendsThePasswordToTheMaintenanceDatabaseOnlyIfTheBindingAllowsIt(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.host.refuseAt = "postgres"
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "is not allowed for postgresql at db2:5432/postgres") {
		t.Errorf("cleanup %q", got.Cleanup)
	}
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran")
	}
}

func TestARestoreTheBindingRefusesFailsBeforeAnyProgramRuns(t *testing.T) {
	refusals := map[string]struct {
		set   func(h *rpHostRig)
		words []string
	}{
		"target": {func(h *rpHostRig) {
			h.host.refused = &sdk.BindingError{Name: rpPasswordRef, Plugin: "postgresql", Target: sdk.Target{Host: "db2", Port: 5432, Database: rpNewDatabase}}
		}, []string{`secret "pg-restore" is not allowed for postgresql at db2:5432/` + rpNewDatabase}},
		"tls": {func(h *rpHostRig) { h.host.grant = sdk.Grant{} }, []string{"tls_mode disable", "--allow-tls disable"}},
	}
	for name, c := range refusals {
		t.Run(name, func(t *testing.T) {
			h := newPGHost(t)
			c.set(h)
			_, f := h.run(h.request())
			hasPrefix(t, f, "SECRET_NOT_ALLOWED (check)")
			mentions(t, f.Error(), c.words...)
			if strings.Contains(f.Error(), rpSecret) {
				t.Errorf("the password leaked: %s", f.Error())
			}
			if len(h.server.ran("psql")) != 0 || len(h.repo.dumped()) != 0 {
				t.Error("something ran")
			}
		})
	}
}

func TestTheRollbackOfAnInterruptedRestoreKeepsToTheBindingToo(t *testing.T) {
	h := newPGHost(t)
	h.server.databases[rpNewDatabase] = 20001
	h.host.refused = &sdk.BindingError{Name: rpPasswordRef, Plugin: "postgresql", Target: sdk.Target{Host: "db2", Port: 5432, Database: rpNewDatabase}}
	got := h.recover(leftByAKill(`,"database":{"name":"` + rpNewDatabase + `","oid":20001}`))
	leftBehind(t, got, []string{"database " + rpNewDatabase}, true)
	if !strings.Contains(got.Cleanup, "is not allowed for postgresql at db2:5432/"+rpNewDatabase) {
		t.Errorf("cleanup %q", got.Cleanup)
	}
	if len(h.server.ran("psql")) != 0 {
		t.Error("psql ran")
	}
	if want := (sdk.Target{Host: "db2", Port: 5432, Database: rpNewDatabase}); len(h.host.asked) != 1 || h.host.asked[0] != want {
		t.Errorf("asked %v, want %v", h.host.asked, want)
	}
}
