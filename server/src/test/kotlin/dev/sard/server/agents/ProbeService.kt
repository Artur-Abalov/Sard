// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import io.grpc.BindableService
import io.grpc.MethodDescriptor
import io.grpc.ServerServiceDefinition
import io.grpc.stub.ServerCalls
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import java.io.ByteArrayInputStream
import java.io.InputStream

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
 * for any service added later. `Whoami` answers with the principal of the call.
 */
class ProbeService : BindableService {
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
            ).build()

    companion object {
        const val SERVICE = "sard.test.v1.Probe"
        const val WHOAMI = "$SERVICE/Whoami"
    }
}

@TestConfiguration(proxyBeanMethods = false)
class ProbeServiceConfiguration {
    @Bean
    fun probeService() = ProbeService()
}
