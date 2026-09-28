// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import io.grpc.BindableService
import io.grpc.MethodDescriptor
import io.grpc.ServerServiceDefinition
import io.grpc.stub.ServerCalls
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import java.io.ByteArrayInputStream
import java.io.InputStream
import java.util.UUID
import kotlin.coroutines.EmptyCoroutineContext
import io.grpc.kotlin.ServerCalls as CoroutineServerCalls

/** Raw bytes on the wire: the probe needs no proto, and proto is not changed for a test. */
object RawBytes : MethodDescriptor.Marshaller<ByteArray> {
    override fun stream(value: ByteArray): InputStream = ByteArrayInputStream(value)

    override fun parse(stream: InputStream): ByteArray = stream.readAllBytes()
}

/** A method descriptor for any full method name, so a test can call what the server binds. */
fun rawMethod(
    fullMethodName: String,
    type: MethodDescriptor.MethodType = MethodDescriptor.MethodType.UNARY,
): MethodDescriptor<ByteArray, ByteArray> =
    MethodDescriptor
        .newBuilder(RawBytes, RawBytes)
        .setFullMethodName(fullMethodName)
        .setType(type)
        .build()

/**
 * A service bound only in tests and absent from [UNAUTHENTICATED_SERVICES]: it stands
 * for any service added later. `Whoami` answers with the principal of the call;
 * `Agents` and `AgentsStream` are coroutine handlers, like AgentService, that hop to
 * another dispatcher and answer with the agent ids a JPA query in the caller's tenant sees.
 */
class ProbeService(
    private val sessions: AgentSessions,
) : BindableService {
    private suspend fun visibleAgents(): ByteArray =
        withContext(Dispatchers.IO) {
            sessions.inTenant { session ->
                session.createSelectionQuery("select id from Agent order by id", UUID::class.java).list()
            }
        }.joinToString(",").toByteArray()

    override fun bindService(): ServerServiceDefinition =
        ServerServiceDefinition
            .builder(SERVICE)
            .addMethod(
                rawMethod(WHOAMI),
                ServerCalls.asyncUnaryCall { _, response ->
                    val principal = AgentPrincipal.KEY.get()
                    val body = principal?.let { "${it.tenantId}/${it.agentId}/${it.serial}" }.orEmpty()
                    response.onNext(body.toByteArray())
                    response.onCompleted()
                },
            ).addMethod(
                CoroutineServerCalls.unaryServerMethodDefinition(EmptyCoroutineContext, rawMethod(AGENTS)) {
                    visibleAgents()
                },
            ).addMethod(
                CoroutineServerCalls.bidiStreamingServerMethodDefinition(
                    EmptyCoroutineContext,
                    rawMethod(AGENTS_STREAM, MethodDescriptor.MethodType.BIDI_STREAMING),
                ) { requests -> requests.map { visibleAgents() } },
            ).build()

    companion object {
        const val SERVICE = "sard.test.v1.Probe"
        const val WHOAMI = "$SERVICE/Whoami"
        const val AGENTS = "$SERVICE/Agents"
        const val AGENTS_STREAM = "$SERVICE/AgentsStream"
    }
}

@TestConfiguration(proxyBeanMethods = false)
class ProbeServiceConfiguration {
    @Bean
    fun probeService(sessions: AgentSessions) = ProbeService(sessions)
}
