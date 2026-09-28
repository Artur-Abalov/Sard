// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.server.registration.RegistrationRejectedException
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import io.grpc.Status.Code.FAILED_PRECONDITION
import io.grpc.Status.Code.INVALID_ARGUMENT
import io.grpc.Status.Code.UNAVAILABLE
import io.grpc.protobuf.StatusProto
import kotlin.test.Test
import kotlin.test.assertEquals
import dev.sard.server.registration.RegistrationRejectedException.Reason as RegisterReason

/** Register's half of the gRPC error model: reason -> code, one ErrorInfo of domain sard.dev with the metadata. */
@MutFlowTest
class RegistrationStatusTest {
    private fun assertMapped(
        reason: RegisterReason,
        code: Status.Code,
        wireReason: String,
        details: Map<String, String> = mapOf("field" to "x"),
    ) {
        val exception = MutFlow.underTest { RegistrationStatus.of(RegistrationRejectedException(reason, details)) }
        assertEquals(code, exception.status.code)
        val status = checkNotNull(StatusProto.fromThrowable(exception))
        assertEquals(1, status.detailsCount)
        val info = status.getDetails(0).unpack(ErrorInfo::class.java)
        assertEquals(wireReason, info.reason)
        assertEquals("sard.dev", info.domain)
        assertEquals(details, info.metadataMap)
        assertEquals("register rejected", status.message)
    }

    @Test
    fun `PROTOCOL_UNSUPPORTED is FAILED_PRECONDITION with the supported range`() {
        val range = mapOf("min_supported" to "1", "max_supported" to "1")
        assertMapped(RegisterReason.PROTOCOL_UNSUPPORTED, FAILED_PRECONDITION, "PROTOCOL_UNSUPPORTED", range)
    }

    @Test
    fun `HOSTNAME_INVALID is INVALID_ARGUMENT`() {
        assertMapped(RegisterReason.HOSTNAME_INVALID, INVALID_ARGUMENT, "HOSTNAME_INVALID")
    }

    @Test
    fun `FIELD_INVALID is INVALID_ARGUMENT`() {
        assertMapped(RegisterReason.FIELD_INVALID, INVALID_ARGUMENT, "FIELD_INVALID")
    }

    @Test
    fun `NAME_INVALID is INVALID_ARGUMENT`() {
        assertMapped(RegisterReason.NAME_INVALID, INVALID_ARGUMENT, "NAME_INVALID")
    }

    @Test
    fun `NAME_DUPLICATE is INVALID_ARGUMENT`() {
        assertMapped(RegisterReason.NAME_DUPLICATE, INVALID_ARGUMENT, "NAME_DUPLICATE")
    }

    @Test
    fun `SNAPSHOT_TOO_LARGE is INVALID_ARGUMENT with the limit`() {
        val details = mapOf("field" to "plugins", "limit" to "64")
        assertMapped(RegisterReason.SNAPSHOT_TOO_LARGE, INVALID_ARGUMENT, "SNAPSHOT_TOO_LARGE", details)
    }

    @Test
    fun `CONFIG_SCHEMA_INVALID is INVALID_ARGUMENT`() {
        assertMapped(RegisterReason.CONFIG_SCHEMA_INVALID, INVALID_ARGUMENT, "CONFIG_SCHEMA_INVALID")
    }

    @Test
    fun `INTERNAL_RETRYABLE is UNAVAILABLE without metadata`() {
        assertMapped(RegisterReason.INTERNAL_RETRYABLE, UNAVAILABLE, "INTERNAL_RETRYABLE", emptyMap())
    }

    @Test
    fun `the reasons are exactly these`() {
        val expected =
            setOf(
                "PROTOCOL_UNSUPPORTED",
                "HOSTNAME_INVALID",
                "FIELD_INVALID",
                "NAME_INVALID",
                "NAME_DUPLICATE",
                "SNAPSHOT_TOO_LARGE",
                "CONFIG_SCHEMA_INVALID",
                "INTERNAL_RETRYABLE",
            )
        assertEquals(expected, RegisterReason.entries.map { it.name }.toSet())
    }
}
