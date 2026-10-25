// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.Sandbox
import dev.sard.proto.agent.v1.VerifierStatus
import dev.sard.server.registration.SandboxImageEntry
import dev.sard.server.registration.SandboxKind
import dev.sard.server.registration.VerifierReport

/**
 * A verifier's status as Register or a heartbeat carries it (V4, V5). Free space above the signed range reads
 * negative on the JVM: unknown, as an unset one; so is an empty runtime version.
 */
fun VerifierStatus.toReport() =
    VerifierReport(
        workFreeBytes = if (hasWorkFreeBytes() && workFreeBytes >= 0) workFreeBytes else null,
        sandbox = sandboxOf(sandbox),
        sandboxVersion = sandboxVersion.ifEmpty { null },
        images = imagesList.map { SandboxImageEntry(it.name, it.version, it.digest) },
    )

private fun sandboxOf(sandbox: Sandbox): SandboxKind? =
    when (sandbox) {
        Sandbox.SANDBOX_NONE -> SandboxKind.NONE
        Sandbox.SANDBOX_DOCKER -> SandboxKind.DOCKER
        Sandbox.SANDBOX_PODMAN -> SandboxKind.PODMAN
        Sandbox.SANDBOX_UNSPECIFIED, Sandbox.UNRECOGNIZED -> null
    }
