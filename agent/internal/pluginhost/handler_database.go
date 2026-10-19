// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// restoreDatabase restores the snapshot into a new database of a server (R2b),
// all or nothing. The password is the secret the plan names, as the step
// started with it.
func (h *handler) restoreDatabase(ctx context.Context, step *agentv1.RunStep, repo restic.Repository, req consolerestore.Request, r executor.Reporter) (*agentv1.StepResult, error) {
	plan := *req.Plan.Postgres
	password, err := h.password(plan.PasswordRef, r)
	if err != nil {
		failure := &consolerestore.Failure{Reason: refusal.SecretInvalid, Stage: consolerestore.StageCheck, Detail: err.Error()}
		return nil, failure
	}
	out, failure := h.console.RestoreDatabase(ctx, repo, consolerestore.DatabaseRequest{
		CommandID: req.CommandID, Repository: req.Repository, SnapshotID: req.SnapshotID, Source: req.Source, Plan: plan, Password: password,
	}, r)
	if failure != nil {
		return failedRestore(failure), failure
	}
	restored := &agentv1.RestoreOutput{Target: out.Database, Database: out.Database, TablesRestored: out.Tables, RolesRestored: out.Roles}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Restore{Restore: restored}}, nil
}

// password is the content of the secret without its line ending, neither empty
// nor holding a NUL byte (F1 ПГ3).
func (h *handler) password(name string, r executor.Reporter) ([]byte, error) {
	value, err := (&host{secrets: h.src.secrets, r: r}).Secret(name)
	if err != nil {
		return nil, err
	}
	return consolerestore.Password(name, value)
}
