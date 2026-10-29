// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func roundTrip(t *testing.T, in *agentv1.BackupOutput) *agentv1.BackupOutput {
	t.Helper()
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := &agentv1.BackupOutput{}
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Scenario: BackupOutput без полей 8 и 9 разбирается как число не сообщено.
func TestBackupOutputWithoutTheUnreadableFieldsParsesAsNotReported(t *testing.T) {
	got := roundTrip(t, &agentv1.BackupOutput{SnapshotId: "s", TotalFiles: 3})
	if got.UnreadableCount != nil || len(got.GetUnreadablePaths()) != 0 {
		t.Errorf("output = %v", got)
	}
}

// Scenario: Число 0 переживает сериализацию и отличимо от отсутствия.
func TestAZeroUnreadableCountSurvivesTheWire(t *testing.T) {
	got := roundTrip(t, &agentv1.BackupOutput{UnreadableCount: proto.Uint64(0)})
	if got.UnreadableCount == nil || got.GetUnreadableCount() != 0 {
		t.Errorf("output = %v", got)
	}
}

// Scenario: Одиннадцать нечитаемых и десять путей переживают сериализацию в своём порядке.
func TestElevenUnreadableAndTenPathsSurviveTheWireInOrder(t *testing.T) {
	var paths []string
	for i := 10; i >= 1; i-- {
		paths = append(paths, fmt.Sprintf("/srv/f%02d", i))
	}
	got := roundTrip(t, &agentv1.BackupOutput{UnreadableCount: proto.Uint64(11), UnreadablePaths: paths})
	if got.GetUnreadableCount() != 11 || !slices.Equal(got.GetUnreadablePaths(), paths) {
		t.Errorf("output = %v", got)
	}
}

// Scenario: Комментарии полей говорят, что это пути без содержимого и что они маскируются.
func TestTheCommentsOfTheUnreadableFieldsStateTheirContract(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "proto", "sard", "agent", "v1", "agent.proto"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)message BackupOutput \{.*?\n\}`).Find(src)
	text := string(m)
	count, paths, ok := strings.Cut(text, "optional uint64 unreadable_count = 8;")
	if m == nil || !ok {
		t.Fatalf("BackupOutput does not declare unreadable_count = 8:\n%s", text)
	}
	_, countDoc, _ := strings.Cut(count, "uint64 total_files = 7;")
	for _, want := range []string{"older", "0 means no unreadable"} {
		if !strings.Contains(countDoc, want) {
			t.Errorf("the comment of unreadable_count lacks %q", want)
		}
	}
	for _, want := range []string{"repeated string unreadable_paths = 9;", "At most 10", "order", "never the", "StepResult.message"} {
		if !strings.Contains(paths, want) {
			t.Errorf("the comment of unreadable_paths lacks %q", want)
		}
	}
}
