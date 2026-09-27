// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// fakeKeys proves the wrapper accepts any crypto.Provider.
type fakeKeys struct{}

func (fakeKeys) Name() string { return "fake" }
func (fakeKeys) RepositoryKey(context.Context, string) (crypto.Key, error) {
	return crypto.Key{}, nil
}

var _ restic.Repository = (*restic.CLI)(nil)

func TestResticStubsAreNotImplemented(t *testing.T) {
	repo := restic.New("restic", "main", "/srv/repo", fakeKeys{})
	if id, err := repo.ID(context.Background()); id != "" || !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("ID = %q, %v", id, err)
	}
	if _, err := repo.Backup(context.Background(), strings.NewReader("x"), nil); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("Backup err = %v", err)
	}
	if err := repo.Restore(context.Background(), "abc", "/tmp/x"); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("Restore err = %v", err)
	}
}
