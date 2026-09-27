// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package crypto_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/crypto"
)

func TestResticAESHandsTheRepositoryPasswordFileToRestic(t *testing.T) {
	p := crypto.NewResticAES(map[string]string{
		"main":    "/etc/sard/main.pass",
		"offsite": "/etc/sard/offsite.pass",
	})
	if p.Name() != "restic-aes" {
		t.Errorf("Name() = %q", p.Name())
	}
	key, err := p.RepositoryKey(context.Background(), "offsite")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key.Env, []string{"RESTIC_PASSWORD_FILE=/etc/sard/offsite.pass"}) {
		t.Errorf("Env = %v", key.Env)
	}
}

func TestResticAESRejectsRepositoriesNotConfiguredOnTheHost(t *testing.T) {
	_, err := crypto.NewResticAES(map[string]string{"main": "/x"}).RepositoryKey(context.Background(), "attacker")
	if !errors.Is(err, crypto.ErrUnknownRepository) || err.Error() != `repository is not configured on this host: "attacker"` {
		t.Fatalf("err = %v", err)
	}
}
