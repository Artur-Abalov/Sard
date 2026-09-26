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

func TestResticAESHandsPasswordFileToRestic(t *testing.T) {
	p := crypto.NewResticAES("/etc/sard/restic.pass")
	if p.Name() != "restic-aes" {
		t.Errorf("Name() = %q", p.Name())
	}
	key, err := p.RepositoryKey(context.Background(), "s3:bucket/host")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key.Env, []string{"RESTIC_PASSWORD_FILE=/etc/sard/restic.pass"}) {
		t.Errorf("Env = %v", key.Env)
	}
}

func TestResticAESWithoutPasswordFileFails(t *testing.T) {
	_, err := crypto.NewResticAES("").RepositoryKey(context.Background(), "repo")
	if !errors.Is(err, crypto.ErrNoPasswordFile) {
		t.Fatalf("err = %v", err)
	}
}
