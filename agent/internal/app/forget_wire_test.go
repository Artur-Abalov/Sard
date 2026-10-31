// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// docs/specs/proto/retention-forget.feature (L1a).

func fieldNames(m protoreflect.MessageDescriptor) []string {
	var names []string
	for i := range m.Fields().Len() {
		names = append(names, string(m.Fields().Get(i).Name()))
	}
	slices.Sort(names)
	return names
}

func TestВзаданииFORGETНетПоляСоСпискомСнимковКУдалению(t *testing.T) {
	got := fieldNames((&agentv1.ForgetTask{}).ProtoReflect().Descriptor())
	want := []string{"incomplete_max_age", "incomplete_snapshot_ids", "keep_snapshot_ids", "policy", "source_id"}
	if !slices.Equal(got, want) {
		t.Errorf("ForgetTask fields = %v, want %v", got, want)
	}
}

func TestВПолитикеХраненияТолькоПравилаResticForgetБезСписковСнимков(t *testing.T) {
	got := fieldNames((&agentv1.RetentionPolicy{}).ProtoReflect().Descriptor())
	want := []string{"keep_daily", "keep_last", "keep_monthly", "keep_weekly", "keep_within", "keep_yearly"}
	if !slices.Equal(got, want) {
		t.Errorf("RetentionPolicy fields = %v, want %v", got, want)
	}
}

// reachable walks the messages that can be reached by fields from m.
func reachable(m protoreflect.MessageDescriptor, seen map[protoreflect.FullName]protoreflect.MessageDescriptor) {
	if _, ok := seen[m.FullName()]; ok {
		return
	}
	seen[m.FullName()] = m
	for i := range m.Fields().Len() {
		if sub := m.Fields().Get(i).Message(); sub != nil {
			reachable(sub, seen)
		}
	}
}

func fromServer() map[protoreflect.FullName]protoreflect.MessageDescriptor {
	seen := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
	reachable((&agentv1.ConnectResponse{}).ProtoReflect().Descriptor(), seen)
	return seen
}

func TestНиОдноСообщениеОтСервераКАгентуНеНесётПределХоста(t *testing.T) {
	for name := range fromServer() {
		if strings.HasSuffix(string(name), ".RetentionFloor") {
			t.Errorf("%s is reachable from ConnectResponse", name)
		}
	}
	if _, ok := fromServer()["sard.agent.v1.ForgetTask"]; !ok {
		t.Error("ForgetTask is not reachable from ConnectResponse: the walk is broken")
	}
}

func TestНиОдноСообщениеОтСервераКАгентуНеНесётПоляОбходаПредела(t *testing.T) {
	for _, m := range fromServer() {
		for i := range m.Fields().Len() {
			name := string(m.Fields().Get(i).Name())
			for _, bad := range []string{"force", "remove_all", "override", "floor", "unsafe"} {
				if strings.Contains(name, bad) {
					t.Errorf("%s.%s contains %q", m.FullName(), name, bad)
				}
			}
		}
	}
}

func TestRepositoryInfoБезПоляПределаРазбираетсяКакОтсутствующийПредел(t *testing.T) {
	var b []byte
	for n, v := range map[protowire.Number]string{1: "base", 2: "local", 3: "5e3f0a9c", 4: "restic-aes"} {
		b = protowire.AppendTag(b, n, protowire.BytesType)
		b = protowire.AppendString(b, v)
	}
	b = protowire.AppendTag(b, 5, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	var info agentv1.RepositoryInfo
	if err := proto.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	if info.RetentionFloor != nil {
		t.Errorf("retention_floor = %v, want absent", info.RetentionFloor)
	}
}

func roundTrip[M proto.Message](t *testing.T, in, out M) M {
	t.Helper()
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestВыключенныйПределОтличаетсяОтОтсутствующего(t *testing.T) {
	in := &agentv1.RepositoryInfo{Name: "main", RetentionFloor: &agentv1.RetentionFloor{
		Disabled: true, Origin: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY}}
	got := roundTrip(t, in, &agentv1.RepositoryInfo{})
	if got.RetentionFloor == nil || !got.RetentionFloor.Disabled || got.RetentionFloor.Origin != in.RetentionFloor.Origin {
		t.Errorf("retention_floor = %v", got.RetentionFloor)
	}
}

func TestRunStepСЗаданиемFORGETПереживаетСериализацию(t *testing.T) {
	in := &agentv1.RunStep{
		Action: agentv1.Action_ACTION_FORGET, RepositoryName: "main",
		Forget: &agentv1.ForgetTask{
			SourceId:              "s1",
			Policy:                &agentv1.RetentionPolicy{KeepDaily: 7, KeepWithin: durationpb.New(336 * time.Hour)},
			KeepSnapshotIds:       []string{"a1"},
			IncompleteSnapshotIds: []string{"b2"},
			IncompleteMaxAge:      durationpb.New(168 * time.Hour),
		},
	}
	got := roundTrip(t, in, &agentv1.RunStep{})
	if !proto.Equal(in, got) {
		t.Errorf("got %v, want %v", got, in)
	}
}

func TestИтогFORGETСПричинойСбояПереживаетСериализацию(t *testing.T) {
	t0 := timestamppb.New(time.Date(2026, 11, 30, 12, 0, 0, 0, time.UTC))
	for _, c := range []struct {
		status agentv1.StepStatus
		fail   agentv1.MaintenanceFailure
		holder string
	}{
		{agentv1.StepStatus_STEP_STATUS_SUCCEEDED, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, ""},
		{agentv1.StepStatus_STEP_STATUS_FAILED, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED, "PID 4242 on db1 by sard-agent"},
		{agentv1.StepStatus_STEP_STATUS_FAILED, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_DELETE_DENIED, ""},
		{agentv1.StepStatus_STEP_STATUS_FAILED, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE, ""},
		{agentv1.StepStatus_STEP_STATUS_CANCELLED, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, ""},
	} {
		in := &agentv1.StepResult{Status: c.status, Output: &agentv1.StepResult_Forget{Forget: &agentv1.ForgetOutput{
			ForgottenSnapshotIds: []string{"a1"}, KeptByFloorSnapshotIds: []string{"b2"}, RemainingSnapshotIds: []string{"b2"},
			ListedAt: t0, Failure: c.fail, LockHolder: c.holder,
		}}}
		if got := roundTrip(t, in, &agentv1.StepResult{}); !proto.Equal(in, got) {
			t.Errorf("%v: got %v, want %v", c.fail, got, in)
		}
	}
}

// docs/specs/agent/retention-forget.feature, rule "Агент сообщает серверу
// предел каждого репозитория".

func TestRegisterНесётПределРепозиторияИЕгоПроисхождение(t *testing.T) {
	const base = "server:\n  address: s:1\nrepositories:\n  - {name: R, url: /srv/r, password_file: /p}\n"
	for name, c := range map[string]struct {
		section string
		want    *agentv1.RetentionFloor
	}{
		"не задан": {"", &agentv1.RetentionFloor{KeepLast: 7, KeepWithin: durationpb.New(336 * time.Hour),
			Origin: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_BUILT_IN}},
		"default": {"retention_floor:\n  default: {keep_last: 10, keep_within_days: 30}\n", &agentv1.RetentionFloor{KeepLast: 10, KeepWithin: durationpb.New(720 * time.Hour),
			Origin: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_DEFAULT}},
		"репозиторий": {"retention_floor:\n  repositories:\n    R: {keep_last: 3, keep_within_days: 2}\n", &agentv1.RetentionFloor{KeepLast: 3, KeepWithin: durationpb.New(48 * time.Hour),
			Origin: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY}},
		"выключен": {"retention_floor:\n  repositories:\n    R: {disabled: true}\n", &agentv1.RetentionFloor{Disabled: true, KeepWithin: durationpb.New(0),
			Origin: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY}},
	} {
		t.Run(name, func(t *testing.T) {
			local, err := config.Parse([]byte(base + c.section))
			if err != nil {
				t.Fatal(err)
			}
			a := newAgent(t, &fakeLink{})
			a.Local = local
			a.RepositoryID = func(context.Context, config.Repository) (string, error) { return "id", nil }
			repos := a.RegisterRequest(context.Background()).GetRepositories()
			if len(repos) != 1 || !proto.Equal(repos[0].GetRetentionFloor(), c.want) {
				t.Errorf("repositories = %v, want retention_floor %v", repos, c.want)
			}
		})
	}
}
