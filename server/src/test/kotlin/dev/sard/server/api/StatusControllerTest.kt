// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.verify.NoRestoreVerifications
import dev.sard.server.verify.RestoreVerifications
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.info.BuildProperties
import java.time.Instant
import java.util.Properties
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

@MutFlowTest
class StatusControllerTest {
    private fun build(version: String?): BuildProperties {
        val props = Properties()
        version?.let { props.setProperty("version", it) }
        return BuildProperties(props)
    }

    @Test
    fun `status carries the build version and the last verified restore`() {
        val at = Instant.parse("2026-09-01T03:00:00Z")
        val verified =
            object : RestoreVerifications {
                override fun lastVerifiedAt() = at
            }
        val status = MutFlow.underTest { StatusController(build("1.2.3"), verified).status() }
        assertEquals(StatusResponse("1.2.3", at), status)
    }

    @Test
    fun `no verification yet means null`() {
        val status = MutFlow.underTest { StatusController(build("1.2.3"), NoRestoreVerifications()).status() }
        assertNull(status.lastVerifiedRestoreAt)
    }

    @Test
    fun `a build without version reports unknown`() {
        val status = MutFlow.underTest { StatusController(build(null), NoRestoreVerifications()).status() }
        assertEquals("unknown", status.version)
    }
}
