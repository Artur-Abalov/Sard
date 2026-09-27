// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.workflow

/** Executes YAML workflows: hook → dump → encrypt → store → verify → notify (roadmap: workflow engine, stage 1). */
interface WorkflowEngine {
    fun start(workflowId: String): String
}
