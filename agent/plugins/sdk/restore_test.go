// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// A destination is a relative path of clean components, none of which starts
// with a dot (names with a leading dot belong to the agent), and a path of
// the snapshot is literal.
func TestRestorePlanValidate(t *testing.T) {
	tests := []struct {
		name    string
		plan    sdk.RestorePlan
		wantErr string // the JSON Pointer of the first violation; "" for a valid plan
	}{
		{"plain destination", sdk.RestorePlan{Destination: "s1/20261010T030000Z"}, ""},
		{"destination with a percent escape", sdk.RestorePlan{Destination: "etc%20data/x"}, ""},
		{"whole snapshot and some paths", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv/data", "/etc/hosts"}}, ""},
		{"empty destination", sdk.RestorePlan{}, "/destination"},
		{"absolute destination", sdk.RestorePlan{Destination: "/etc"}, "/destination"},
		{"parent component", sdk.RestorePlan{Destination: "s1/../../etc"}, "/destination"},
		{"dot component", sdk.RestorePlan{Destination: "s1/./x"}, "/destination"},
		{"empty component", sdk.RestorePlan{Destination: "s1//x"}, "/destination"},
		{"trailing slash", sdk.RestorePlan{Destination: "s1/"}, "/destination"},
		{"reserved staging name", sdk.RestorePlan{Destination: ".sard-staging/c1"}, "/destination"},
		{"hidden component", sdk.RestorePlan{Destination: "s1/.hidden"}, "/destination"},
		{"NUL byte", sdk.RestorePlan{Destination: "s1\x00x"}, "/destination"},
		{"4096 bytes", sdk.RestorePlan{Destination: strings.Repeat("a/", 2047) + "aa"}, ""},
		{"4097 bytes", sdk.RestorePlan{Destination: strings.Repeat("a/", 2047) + "aaa"}, "/destination"},
		{"component of 255 bytes", sdk.RestorePlan{Destination: strings.Repeat("a", 255)}, ""},
		{"component of 256 bytes", sdk.RestorePlan{Destination: strings.Repeat("a", 256)}, "/destination"},
		{"component of 128 two-byte characters", sdk.RestorePlan{Destination: strings.Repeat("ж", 128)}, "/destination"},
		{"relative path in the snapshot", sdk.RestorePlan{Destination: "x", Paths: []string{"srv/data"}}, "/paths/0"},
		{"wildcard star", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv/*"}}, "/paths/0"},
		{"wildcard question mark", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv/?"}}, "/paths/0"},
		{"wildcard bracket", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv/[a]"}}, "/paths/0"},
		{"backslash", sdk.RestorePlan{Destination: "x", Paths: []string{`/srv/\x`}}, "/paths/0"},
		{"parent component in the snapshot", sdk.RestorePlan{Destination: "x", Paths: []string{"/ok", "/srv/../etc"}}, "/paths/1"},
		{"repeated path", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv/data", "/srv/data"}}, "/paths/1"},
		{"NUL in a path", sdk.RestorePlan{Destination: "x", Paths: []string{"/srv\x00"}}, "/paths/0"},
		{"empty path", sdk.RestorePlan{Destination: "x", Paths: []string{""}}, "/paths/0"},
		{"path of 4096 bytes", sdk.RestorePlan{Destination: "x", Paths: []string{"/" + strings.Repeat("a", 4095)}}, ""},
		{"path of 4097 bytes", sdk.RestorePlan{Destination: "x", Paths: []string{"/" + strings.Repeat("a", 4096)}}, "/paths/0"},
		{"100 paths", sdk.RestorePlan{Destination: "x", Paths: numbered(100)}, ""},
		{"101 paths", sdk.RestorePlan{Destination: "x", Paths: numbered(101)}, "/paths"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var cfgErr *sdk.ConfigError
			if !errors.As(err, &cfgErr) || !errors.Is(err, sdk.ErrInvalidConfig) {
				t.Fatalf("Validate() = %v, want a *ConfigError", err)
			}
			if got := cfgErr.Violations[0].Path; got != tt.wantErr {
				t.Errorf("first violation at %q, want %q (%v)", got, tt.wantErr, err)
			}
		})
	}
}

func numbered(n int) []string {
	paths := make([]string, n)
	for i := range paths {
		paths[i] = "/p" + strings.Repeat("x", i%7) + string(rune('a'+i/26)) + string(rune('a'+i%26))
	}
	return paths
}

// A violation says what is wrong, not just where: the message is what an
// operator reads in the console.
func TestRestorePlanViolationMessages(t *testing.T) {
	tests := []struct {
		name     string
		plan     sdk.RestorePlan
		wantPath string
		wantMsg  string
	}{
		{"empty destination", sdk.RestorePlan{}, "/destination", "want 1-4096 bytes, got 0"},
		{"absolute destination", sdk.RestorePlan{Destination: "/etc"}, "/destination", "must be a relative path inside the restore directory of the host"},
		{"empty path", sdk.RestorePlan{Destination: "x", Paths: []string{""}}, "/paths/0", "want 1-4096 bytes, got 0"},
		{"repeat of a valid path", sdk.RestorePlan{Destination: "x", Paths: []string{"/a", "/a"}}, "/paths/1", "repeats an earlier path"},
		{"repeat of an invalid path keeps its own reason", sdk.RestorePlan{Destination: "x", Paths: []string{"a", "a"}}, "/paths/1", "must be an absolute path of the snapshot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfgErr *sdk.ConfigError
			if err := tt.plan.Validate(); !errors.As(err, &cfgErr) {
				t.Fatalf("Validate() = %v, want a *ConfigError", err)
			}
			last := cfgErr.Violations[len(cfgErr.Violations)-1]
			if last.Path != tt.wantPath || last.Message != tt.wantMsg {
				t.Errorf("last violation = %q %q, want %q %q", last.Path, last.Message, tt.wantPath, tt.wantMsg)
			}
		})
	}
}

// A restore into a new database of a server has a name instead of a
// destination: 1-63 bytes without NUL, and it neither names a directory nor
// paths of a snapshot.
func TestRestorePlanOfADatabase(t *testing.T) {
	db := func(name string) sdk.RestorePlan {
		return sdk.RestorePlan{Postgres: &sdk.PostgresRestore{Host: "db2", Port: 5432, User: "restorer", Name: name}}
	}
	withDestination := db("app")
	withDestination.Destination = "x"
	withPaths := db("app")
	withPaths.Paths = []string{"/srv"}
	tests := []struct {
		name    string
		plan    sdk.RestorePlan
		wantErr string
	}{
		{"plain name", db("app_restored_20261011"), ""},
		{"name with a quote and a space", db(`x"; DROP DATABASE app; --`), ""},
		{"name of 1 byte", db("a"), ""},
		{"name of 2 bytes", db("ab"), ""},
		{"name of 63 bytes", db(strings.Repeat("a", 63)), ""},
		{"name of 64 bytes", db(strings.Repeat("a", 64)), "/database"},
		{"name of 32 two-byte characters", db(strings.Repeat("ж", 32)), "/database"},
		{"empty name", db(""), "/database"},
		{"NUL in the name", db("a\x00b"), "/database"},
		{"a destination as well", withDestination, "/destination"},
		{"paths as well", withPaths, "/paths"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var cfgErr *sdk.ConfigError
			if !errors.As(err, &cfgErr) || cfgErr.Violations[0].Path != tt.wantErr {
				t.Fatalf("Validate() = %v, want a violation at %q", err, tt.wantErr)
			}
		})
	}
}
