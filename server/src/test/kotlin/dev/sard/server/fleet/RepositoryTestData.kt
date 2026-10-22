// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import java.time.Instant
import java.util.UUID

internal val REGISTERED_AT = Instant.parse("2026-10-21T12:00:00Z")
internal val A = UUID.fromString("0192f7a0-0000-7000-8000-00000000000a")
internal val B = UUID.fromString("a192f7a0-0000-7000-8000-00000000000b")
internal val W = UUID.fromString("3f2a9c1e-0000-7000-8000-00000000000c")
internal const val R1 = "1111"
internal const val R2 = "2222"

internal fun holder(
    agent: UUID,
    repositoryId: String = R1,
    name: String = "base",
    key: RecoveryKey = RecoveryKey.PRESENT,
    at: Instant = REGISTERED_AT,
    role: AgentRole = AgentRole.SOURCE,
    builtin: Boolean = false,
    hostname: String = "host-$agent".take(10),
) = RepositoryHolder(agent, hostname, role, builtin, repositoryId, name, at, key)
