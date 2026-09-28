// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import org.springframework.context.ApplicationListener
import org.springframework.grpc.server.lifecycle.GrpcServerShutdownEvent

/**
 * Spring gRPC publishes this right before `Server.shutdown()`, which then waits for open calls:
 * closing the streams here ends them with a reason at once instead of at the grace period.
 */
class AgentStreamShutdown(
    private val streams: AgentStreams,
) : ApplicationListener<GrpcServerShutdownEvent> {
    override fun onApplicationEvent(event: GrpcServerShutdownEvent) = streams.shutdown()
}
