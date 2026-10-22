// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func file(name string) sdk.ExtractFile {
	return sdk.ExtractFile{SnapshotID: strings.Repeat("a", 64), Path: "/" + name, Name: name}
}

// A plan the host lays out lists files that lie in the root of the target
// under names the host can trust: the plugin names them, the host writes them.
func TestAnExtractPlanIsValidWhenEveryFileHasASafeUniqueName(t *testing.T) {
	long := strings.Repeat("n", 255)
	tests := []struct {
		name    string
		plan    sdk.ExtractPlan
		wantErr string
	}{
		{"no files: restored as a tree", sdk.ExtractPlan{}, ""},
		{"one file", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("out.bin")}}, ""},
		{"two files", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("a"), file("b")}}, ""},
		{"a name of 255 bytes", sdk.ExtractPlan{Files: []sdk.ExtractFile{file(long)}}, ""},
		{"a name of 256 bytes", sdk.ExtractPlan{Files: []sdk.ExtractFile{file(long + "n")}}, "name"},
		{"an empty name", sdk.ExtractPlan{Files: []sdk.ExtractFile{{SnapshotID: "x", Path: "/a"}}}, "name"},
		{"dot", sdk.ExtractPlan{Files: []sdk.ExtractFile{file(".")}}, "name"},
		{"dot dot", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("..")}}, "name"},
		{"a slash", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("a/b")}}, "name"},
		{"a NUL", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("a\x00b")}}, "name"},
		{"the same name twice", sdk.ExtractPlan{Files: []sdk.ExtractFile{file("a"), file("a")}}, "twice"},
		{"no snapshot", sdk.ExtractPlan{Files: []sdk.ExtractFile{{Path: "/a", Name: "a"}}}, "snapshot"},
		{"no path", sdk.ExtractPlan{Files: []sdk.ExtractFile{{SnapshotID: "x", Name: "a"}}}, "path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Validate() = %v, want an error naming %q", err, tt.wantErr)
			}
		})
	}
}
