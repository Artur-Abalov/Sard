// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"reflect"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Scenario: Плагин не удаляет записи сам. The journal the SDK gives a plugin,
// while it restores and while it recovers, can only save the entry of its command.
func TestTheJournalOfAPluginCanOnlySave(t *testing.T) {
	journal := reflect.TypeFor[sdk.Journal]()
	if journal.NumMethod() != 1 || journal.Method(0).Name != "Save" {
		t.Fatalf("Journal has %d methods, the first is %s: a plugin may only Save", journal.NumMethod(), journal.Method(0).Name)
	}
}

func TestBothHostsGiveThePluginThatJournalAndNoWayToListOrRemoveEntries(t *testing.T) {
	journal := reflect.TypeFor[sdk.Journal]()
	for _, host := range []reflect.Type{reflect.TypeFor[sdk.RestoreHost](), reflect.TypeFor[sdk.RecoveryHost]()} {
		m, ok := host.MethodByName("Journal")
		if !ok || m.Type.NumOut() != 1 || m.Type.Out(0) != journal {
			t.Errorf("%s: Journal() does not give a sdk.Journal", host)
		}
		for _, name := range []string{"List", "Read", "Remove", "Delete", "Entries"} {
			if _, ok := host.MethodByName(name); ok {
				t.Errorf("%s has %s", host, name)
			}
		}
	}
}

// The phases are told to the server by number: zero is "none", and the
// restore comes after the preparation.
func TestThePhasesAreNumberedFromOneInTheOrderTheyHappen(t *testing.T) {
	if sdk.PhasePreparing != 1 || sdk.PhaseRestoring != 2 {
		t.Errorf("PhasePreparing = %d, PhaseRestoring = %d, want 1 and 2", sdk.PhasePreparing, sdk.PhaseRestoring)
	}
}
