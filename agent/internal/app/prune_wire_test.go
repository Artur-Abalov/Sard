// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// docs/specs/proto/retention-prune.feature (L1 release 1).

func TestВЗаданииPRUNEНетНиОдногоПоля(t *testing.T) {
	if got := fieldNames((&agentv1.PruneTask{}).ProtoReflect().Descriptor()); len(got) != 0 {
		t.Errorf("PruneTask fields = %v, want none", got)
	}
}

func TestНиОдноСообщениеОтСервераКАгентуСШагомPRUNEНеНесётПоляОбхода(t *testing.T) {
	seen := fromServer()
	if _, ok := seen["sard.agent.v1.PruneTask"]; !ok {
		t.Error("PruneTask is not reachable from ConnectResponse: the walk is broken")
	}
	for _, m := range seen {
		for i := range m.Fields().Len() {
			name := string(m.Fields().Get(i).Name())
			for _, bad := range []string{"force", "remove_all", "override", "floor", "unsafe", "max_unused", "repack"} {
				if strings.Contains(name, bad) {
					t.Errorf("%s.%s contains %q", m.FullName(), name, bad)
				}
			}
		}
	}
}

func TestИтогPRUNEПереживаетСериализацию(t *testing.T) {
	for _, c := range []struct {
		status      agentv1.StepStatus
		freed, size uint64
		fail        agentv1.MaintenanceFailure
		holder      string
	}{
		{agentv1.StepStatus_STEP_STATUS_SUCCEEDED, 1073741824, 53687091200, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, ""},
		{agentv1.StepStatus_STEP_STATUS_SUCCEEDED, 0, 0, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, ""},
		{agentv1.StepStatus_STEP_STATUS_FAILED, 0, 0, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED, "PID 4242 on db1 by sard-agent"},
		{agentv1.StepStatus_STEP_STATUS_FAILED, 0, 0, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_DELETE_DENIED, ""},
		{agentv1.StepStatus_STEP_STATUS_FAILED, 0, 0, agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE, ""},
	} {
		in := &agentv1.StepResult{Status: c.status, Output: &agentv1.StepResult_Prune{Prune: &agentv1.PruneOutput{
			FreedBytes: proto.Uint64(c.freed), RepositorySizeBytes: proto.Uint64(c.size), Failure: c.fail, LockHolder: c.holder,
		}}}
		got := roundTrip(t, in, &agentv1.StepResult{})
		if !proto.Equal(in, got) || got.GetPrune().FreedBytes == nil || got.GetPrune().RepositorySizeBytes == nil {
			t.Errorf("%v: got %v, want %v", c.fail, got, in)
		}
	}
}

func TestИтогPRUNEБезСводкиResticОтличимОтНулевойСводки(t *testing.T) {
	in := &agentv1.StepResult{Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED, Output: &agentv1.StepResult_Prune{Prune: &agentv1.PruneOutput{}}}
	got := roundTrip(t, in, &agentv1.StepResult{}).GetPrune()
	if got == nil || got.FreedBytes != nil || got.RepositorySizeBytes != nil {
		t.Errorf("output = %v, want present without freed_bytes and repository_size_bytes", got)
	}
}

func TestRunStepСЗаданиемPRUNEПереживаетСериализацию(t *testing.T) {
	in := &agentv1.RunStep{Action: agentv1.Action_ACTION_PRUNE, RepositoryName: "main",
		Timeout: durationpb.New(12 * time.Hour), Prune: &agentv1.PruneTask{}}
	got := roundTrip(t, in, &agentv1.RunStep{})
	if got.Action != in.Action || got.RepositoryName != "main" || got.Timeout.AsDuration() != 12*time.Hour || got.Prune == nil {
		t.Errorf("got %v, want %v", got, in)
	}
}

func TestRegisterСоВстроеннымиДействиямиПереживаетСериализацию(t *testing.T) {
	in := &agentv1.RegisterRequest{BuiltinActions: []agentv1.Action{agentv1.Action_ACTION_FORGET, agentv1.Action_ACTION_PRUNE}}
	got := roundTrip(t, in, &agentv1.RegisterRequest{})
	if len(got.BuiltinActions) != 2 || got.BuiltinActions[0] != agentv1.Action_ACTION_FORGET || got.BuiltinActions[1] != agentv1.Action_ACTION_PRUNE {
		t.Errorf("builtin_actions = %v", got.BuiltinActions)
	}
}

func TestRegisterАгентаБезПоляВстроенныхДействийЧитаетсяКакПустойСписок(t *testing.T) {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendString(b, "db1")
	b = protowire.AppendTag(b, 13, protowire.BytesType)
	b = protowire.AppendBytes(b, nil)
	var req agentv1.RegisterRequest
	if err := proto.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.BuiltinActions) != 0 {
		t.Errorf("builtin_actions = %v, want empty", req.BuiltinActions)
	}
}

// docs/specs/agent/retention-prune.feature, rule "Агент сообщает действия,
// которые выполняет без плагина".

type noHandler struct{ executor.Handler }

func registerWith(t *testing.T, r role.Role) []agentv1.Action {
	t.Helper()
	a := newAgent(t, &fakeLink{})
	a.Role = r
	a.Builtin = executor.Builtin{
		agentv1.Action_ACTION_PRUNE:  noHandler{},
		agentv1.Action_ACTION_FORGET: noHandler{},
	}
	return a.RegisterRequest(context.Background()).GetBuiltinActions()
}

func TestАгентРолиSourceСообщаетFORGETИPRUNEКакВстроенныеДействия(t *testing.T) {
	got := registerWith(t, role.Source)
	if len(got) != 2 || got[0] != agentv1.Action_ACTION_FORGET || got[1] != agentv1.Action_ACTION_PRUNE {
		t.Errorf("builtin_actions = %v", got)
	}
}

func TestВерификаторНеСообщаетВстроенныхДействий(t *testing.T) {
	if got := registerWith(t, role.Verifier); len(got) != 0 {
		t.Errorf("builtin_actions = %v", got)
	}
}
