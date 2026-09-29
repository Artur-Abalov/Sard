// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

type fake struct{ name, version, schema string }

var _ sdk.Plugin = fake{}

func plugin(name string) fake { return fake{name: name, version: "1.0.0", schema: `{}`} }

func (f fake) Name() string         { return f.name }
func (f fake) Version() string      { return f.version }
func (f fake) ConfigSchema() []byte { return []byte(f.schema) }

func (fake) Prepare(context.Context, sdk.Host, sdk.Config) error { return nil }

func (fake) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) { return sdk.Dump{}, nil }

func (fake) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error { return nil }

func TestRegistryLooksUpByNameAndListsNamesSorted(t *testing.T) {
	r, err := sdk.NewRegistry(plugin("mysql"), plugin("files"), plugin("postgresql"))
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
	_, err := sdk.NewRegistry(plugin("files"), plugin("files"))
	if !errors.Is(err, sdk.ErrDuplicateName) || err.Error() != `duplicate plugin name: "files"` {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryRejectsEmptyNames(t *testing.T) {
	if _, err := sdk.NewRegistry(plugin("files"), plugin("")); !errors.Is(err, sdk.ErrEmptyName) {
		t.Fatalf("err = %v", err)
	}
}

// The limits are those of sard-server's Register (S4a):
// server/src/main/kotlin/dev/sard/server/registration/SnapshotRules.kt.
func TestRegistryAcceptsWhatRegisterAccepts(t *testing.T) {
	longest := "a" + strings.Repeat("-", 127)
	version := strings.Repeat("~", 64)
	schema := `{"description":"` + strings.Repeat("x", sdk.MaxConfigSchemaBytes-18) + `"}`
	if len(schema) != sdk.MaxConfigSchemaBytes {
		t.Fatalf("test schema is %d bytes", len(schema))
	}
	if _, err := sdk.NewRegistry(fake{longest, version, schema}, fake{"A0._-", "!", `true`}); err != nil {
		t.Fatal(err)
	}
	many := make([]sdk.Plugin, sdk.MaxPlugins)
	for i := range many {
		many[i] = plugin("p" + strconv.Itoa(i))
	}
	if _, err := sdk.NewRegistry(many...); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRejectsWhatRegisterRejects(t *testing.T) {
	tooMany := make([]sdk.Plugin, sdk.MaxPlugins+1)
	for i := range tooMany {
		tooMany[i] = plugin("p" + strconv.Itoa(i))
	}
	for _, tc := range []struct {
		name    string
		plugins []sdk.Plugin
		want    error
		message string
	}{
		{"name starts with a dot", []sdk.Plugin{plugin(".files")}, sdk.ErrInvalidName, `invalid plugin name: ".files"`},
		{"name with a space", []sdk.Plugin{plugin("my files")}, sdk.ErrInvalidName, ""},
		{"name of 129 characters", []sdk.Plugin{plugin(strings.Repeat("a", 129))}, sdk.ErrInvalidName, ""},
		{"empty version", []sdk.Plugin{fake{"files", "", `{}`}}, sdk.ErrInvalidVersion, `plugin "files": invalid version: ""`},
		{"version with a space", []sdk.Plugin{fake{"files", "1.0 beta", `{}`}}, sdk.ErrInvalidVersion, ""},
		{"version of 65 characters", []sdk.Plugin{fake{"files", strings.Repeat("1", 65), `{}`}}, sdk.ErrInvalidVersion, ""},
		{"non-ASCII version", []sdk.Plugin{fake{"files", "1.0é", `{}`}}, sdk.ErrInvalidVersion, ""},
		{"schema not JSON", []sdk.Plugin{fake{"files", "1", `{`}}, sdk.ErrInvalidSchema, `plugin "files": config schema is not one JSON value of at most 65536 bytes without NUL characters`},
		{"empty schema", []sdk.Plugin{fake{"files", "1", ``}}, sdk.ErrInvalidSchema, ""},
		{"two JSON values", []sdk.Plugin{fake{"files", "1", `{} {}`}}, sdk.ErrInvalidSchema, ""},
		{"NUL character", []sdk.Plugin{fake{"files", "1", `{"title":"a\u0000"}`}}, sdk.ErrInvalidSchema, ""},
		{"schema too large", []sdk.Plugin{fake{"files", "1", `"` + strings.Repeat("x", sdk.MaxConfigSchemaBytes-1) + `"`}}, sdk.ErrInvalidSchema, ""},
		{"65 plugins", tooMany, sdk.ErrTooManyPlugins, "more than 64 plugins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sdk.NewRegistry(tc.plugins...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.message != "" && err.Error() != tc.message {
				t.Errorf("Error() = %q, want %q", err.Error(), tc.message)
			}
		})
	}
}

func TestErrNotImplementedMessage(t *testing.T) {
	if sdk.ErrNotImplemented.Error() != "not implemented" {
		t.Fatal(sdk.ErrNotImplemented)
	}
}
