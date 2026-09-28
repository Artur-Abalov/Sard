// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import org.springframework.context.ApplicationListener
import org.springframework.grpc.server.lifecycle.GrpcServerLifecycleEvent
import org.springframework.grpc.server.lifecycle.GrpcServerShutdownEvent
import org.springframework.grpc.server.lifecycle.GrpcServerStartedEvent

/**
 * Follows the gRPC server's lifecycle. Spring gRPC publishes the shutdown event right before
 * `Server.shutdown()`, which then waits for open calls: closing the streams here ends them with
 * a reason at once instead of at the grace period. A stopped server can start again (Spring's
 * lifecycle restarts it, e.g. when a cached test context resumes): then streams are accepted again.
 */
class AgentStreamShutdown(
    private val streams: AgentStreams,
) : ApplicationListener<GrpcServerLifecycleEvent> {
    override fun onApplicationEvent(event: GrpcServerLifecycleEvent) {
        if (event is GrpcServerShutdownEvent) streams.shutdown()
        if (event is GrpcServerStartedEvent) streams.reopen()
    }
}
