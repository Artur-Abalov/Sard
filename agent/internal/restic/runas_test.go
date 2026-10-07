// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Р6: under sudo restic runs as the service user, version and every
// repository command alike.
func TestEveryResticRunCarriesTheUserItRunsAs(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig, "init": {stdout: "init.json"}, "version": {stdout: "version.txt"}})
	runAs := &restic.RunAs{UID: 990, GID: 991}
	cli := restic.New(restic.Options{Exec: f.exec, Keys: f.keys, ReadFile: f.files.read, RunAs: runAs}, f.repo)
	ctx := context.Background()
	_, _ = cli.ID(ctx)
	_, _ = cli.Init(ctx)
	_ = cli.Check(ctx)
	if len(f.exec.calls) != 3 {
		t.Fatalf("calls %d", len(f.exec.calls))
	}
	for _, c := range f.exec.calls {
		if c.RunAs == nil || *c.RunAs != *runAs {
			t.Errorf("%v ran as %+v", c.Args, c.RunAs)
		}
	}
}

func TestWithoutRunAsResticRunsAsTheCaller(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	_, _ = f.build().ID(context.Background())
	if f.exec.calls[0].RunAs != nil {
		t.Fatalf("ran as %+v", f.exec.calls[0].RunAs)
	}
}

func TestCredentialIsTheUserAndGroupWithoutSupplementaryGroups(t *testing.T) {
	c := restic.CredentialFor(&restic.RunAs{UID: 990, GID: 991})
	if c.Uid != 990 || c.Gid != 991 || len(c.Groups) != 0 || c.NoSetGroups {
		t.Fatalf("credential %+v", c)
	}
	if restic.CredentialFor(nil) != nil {
		t.Fatal("a credential for nobody")
	}
}
