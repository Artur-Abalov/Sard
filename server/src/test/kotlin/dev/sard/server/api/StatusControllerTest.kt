// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.Edition
import dev.sard.server.extension.ReportedEdition
import dev.sard.server.verify.RestoreVerifications
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.info.BuildProperties
import java.time.Instant
import java.util.Properties
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val FREE = ReportedEdition(Edition.FREE)

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
        val status = MutFlow.underTest { StatusController(build("1.2.3"), verified, FREE).status() }
        assertEquals(StatusResponse("1.2.3", at, EditionInfo("free", "Free")), status)
    }

    @Test
    fun `no verification yet means null`() {
        val controller = StatusController(build("1.2.3"), RestoreVerifications { null }, FREE)
        val status = MutFlow.underTest { controller.status() }
        assertNull(status.lastVerifiedRestoreAt)
    }

    @Test
    fun `a build without version reports unknown`() {
        val status = MutFlow.underTest { StatusController(build(null), RestoreVerifications { null }, FREE).status() }
        assertEquals("unknown", status.version)
    }
}
