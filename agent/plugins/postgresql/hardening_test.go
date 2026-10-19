// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A step cancelled while the identifier of the cluster is read is cancelled,
// not a backup without the tag.
func TestACancelWhileTheClusterIdentifierIsReadStopsThePreparation(t *testing.T) {
	d := newDirect()
	cause := errors.New("cancelled while the identifier is read")
	ctx, cancel := context.WithCancelCause(context.Background())
	d.proc.on("psql", func(c context.Context, cmd plugCmd) (int, error) {
		for i, a := range cmd.Args {
			if a == "-c" && strings.Contains(cmd.Args[i+1], "pg_control_system") {
				cancel(cause)
				<-c.Done()
				return -1, errTerminated
			}
		}
		return say(psqlRow(160004, false, true, "backup"), nil, 0)(c, cmd)
	})
	if err := d.plugin.Prepare(ctx, d.host, cfgOf(kg())); !errors.Is(err, cause) {
		t.Errorf("Prepare: %v", err)
	}
}

func TestOnlyDigitsMakeAClusterIdentifier(t *testing.T) {
	for answer, recorded := range map[string]bool{
		"0123456789": true, "7": true, "": false, "123/": false, "123:": false, "12 3": false, "-1": false, "1e5": false,
	} {
		r := newRig(t)
		r.proc.on("psql", byQuery(answer, ""))
		want(t, r.backup(k()), succeeded)
		got := false
		for _, tag := range tagsOf(r.restic.backups()[0]) {
			got = got || strings.HasPrefix(tag, "postgresql.system_identifier=")
		}
		if got != recorded {
			t.Errorf("%q: recorded %v, want %v", answer, got, recorded)
		}
		if told := strings.Contains(r.sink.text(), "system identifier of the cluster is not recorded"); told == recorded {
			t.Errorf("%q: the log tells of the missing identifier: %v\n%s", answer, told, r.sink.text())
		}
	}
}

// A psql that failed says why it failed, not that its answer was odd.
func TestAFailedQueryOfTheIdentifierIsToldByItsError(t *testing.T) {
	r := newRig(t)
	r.proc.on("psql", func(ctx context.Context, c plugCmd) (int, error) {
		for i, a := range c.Args {
			if a == "-c" && strings.Contains(c.Args[i+1], "pg_control_system") {
				return say("garbage\n", []string{"psql: error: connection lost"}, 2)(ctx, c)
			}
		}
		return say(psqlRow(160004, false, true, "backup"), nil, 0)(ctx, c)
	})
	want(t, r.backup(k()), succeeded)
	text := r.sink.text()
	if !strings.Contains(text, "connection lost") || strings.Contains(text, "unexpected answer of psql") {
		t.Errorf("log:\n%s", text)
	}
}
