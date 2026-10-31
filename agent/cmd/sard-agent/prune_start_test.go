// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/retention-prune.feature (L1 release 1): a PRUNE step goes
// through the started agent, and Register names the builtin actions.

func TestАгентРолиSourceСообщаетВстроенныеДействияСерверу(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("source")...)
	if code, _, stderr := h.registered(1); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	want := []agentv1.Action{agentv1.Action_ACTION_FORGET, agentv1.Action_ACTION_PRUNE}
	if got := h.server.registerRequests()[0].GetBuiltinActions(); !slices.Equal(got, want) {
		t.Errorf("builtin_actions = %v, want %v", got, want)
	}
}

func TestВерификаторНеСообщаетСерверуВстроенныхДействий(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("verifier")...)
	if code, _, stderr := h.registered(1); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := h.server.registerRequests()[0].GetBuiltinActions(); len(got) != 0 {
		t.Errorf("builtin_actions = %v, want none", got)
	}
}

func TestШагPRUNEОтСервераДоходитДоАгентаИИтогВозвращается(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("source")...)
	writeFile(t, filepath.Join(h.dir, "qa.pass"), []byte("pw"), 0o600)
	h.extra = "repositories:\n  - {name: qa, url: " + h.dir + "/repo, password_file: " + h.dir + "/qa.pass}\n"
	script := "#!/bin/sh\necho \"$1\" >> " + h.resticCalls + "\n" +
		"case \"$1\" in\n" +
		"prune) echo 'total prune:        1000 blobs / 1.000 GiB'; echo 'remaining:         50000 blobs / 50.000 GiB' ;;\n" +
		"unlock) ;;\n" +
		"*) echo 'restic 0.19.1 compiled with go1.26.4 on linux/amd64' ;;\nesac\n"
	h.restic = "restic: {path: " + writeScript(t, filepath.Join(h.dir, "restic"), script) + "}\n"
	h.server.steps = []*agentv1.RunStep{{CommandId: "p1", Action: agentv1.Action_ACTION_PRUNE, RepositoryName: "qa", Prune: &agentv1.PruneTask{}}}
	code, _, stderr := h.runUntil(15*time.Second, []string{"--config", h.config()}, func() bool { return len(h.server.stepResults()) > 0 })
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	requirePruneOutput(t, h.server.stepResults()[0])
	calls := strings.Fields(fileContent(t, h.resticCalls))
	if !slices.Contains(calls, "unlock") || !slices.Contains(calls, "prune") || slices.Contains(calls, "forget") {
		t.Errorf("restic was run as %q", calls)
	}
}

func requirePruneOutput(t *testing.T, r *agentv1.StepResult) {
	t.Helper()
	out := r.GetPrune()
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED || out == nil {
		t.Fatalf("result = %v", r)
	}
	if out.GetFreedBytes() != 1073741824 || out.GetRepositorySizeBytes() != 53687091200 {
		t.Errorf("output = %v", out)
	}
}
