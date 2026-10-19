// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.ProbeResponse
import java.time.Clock

/**
 * The answer to AgentService.Probe (A8d-3): the server clock and nothing else. Read-only by
 * construction: it holds no store, session or stream, so it cannot change the agent's state.
 */
class AgentProbe(
    private val clock: Clock,
) {
    fun answer(): ProbeResponse {
        val now = clock.instant()
        return ProbeResponse
            .newBuilder()
            .setServerTime(Timestamp.newBuilder().setSeconds(now.epochSecond).setNanos(now.nano))
            .build()
    }
}
