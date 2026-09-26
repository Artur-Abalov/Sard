// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

type fake struct{ name string }

var _ sdk.Plugin = fake{}

func (f fake) Name() string                                     { return f.name }
func (fake) ConfigSchema() []byte                               { return []byte(`{}`) }
func (fake) Prepare(context.Context, sdk.Config) error          { return nil }
func (fake) Dump(context.Context, sdk.Config) (sdk.Dump, error) { return sdk.Dump{}, nil }
func (fake) Stream(context.Context, sdk.Dump, io.Writer) error  { return nil }
func (fake) Verify(context.Context, sdk.Config, string) error   { return nil }

func TestRegistryLooksUpByNameAndListsNamesSorted(t *testing.T) {
	r, err := sdk.NewRegistry(fake{"mysql"}, fake{"files"}, fake{"postgresql"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Names(); !slices.Equal(got, []string{"files", "mysql", "postgresql"}) {
		t.Errorf("Names() = %v", got)
	}
	p, ok := r.Get("mysql")
	if !ok || p.Name() != "mysql" {
		t.Errorf("Get(mysql) = %v, %v", p, ok)
	}
	if _, ok := r.Get("oracle"); ok {
		t.Error("Get(oracle) found a plugin that was never registered")
	}
}

func TestEmptyRegistry(t *testing.T) {
	r, err := sdk.NewRegistry()
	if err != nil || len(r.Names()) != 0 {
		t.Fatalf("NewRegistry() = %v, %v", r, err)
	}
}

func TestRegistryRejectsDuplicateNames(t *testing.T) {
	_, err := sdk.NewRegistry(fake{"files"}, fake{"files"})
	if !errors.Is(err, sdk.ErrDuplicateName) || err.Error() != `duplicate plugin name: "files"` {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryRejectsEmptyNames(t *testing.T) {
	if _, err := sdk.NewRegistry(fake{"files"}, fake{""}); !errors.Is(err, sdk.ErrEmptyName) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrNotImplementedMessage(t *testing.T) {
	if sdk.ErrNotImplemented.Error() != "not implemented" {
		t.Fatal(sdk.ErrNotImplemented)
	}
}
