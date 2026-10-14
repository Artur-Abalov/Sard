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
