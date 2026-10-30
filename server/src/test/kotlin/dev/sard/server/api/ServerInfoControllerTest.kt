// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.Edition
import dev.sard.server.extension.EditionProvider
import dev.sard.server.extension.ExtensionRegistry
import dev.sard.server.extension.LicenceItem
import dev.sard.server.extension.LicenceState
import dev.sard.server.extension.LicenceSummary
import dev.sard.server.extension.ReportedEdition
import dev.sard.server.extension.SardExtension
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val PRO = Edition("pro", "Pro")
private val UNAVAILABLE = LicenceInfo(LicenceStatus.UNAVAILABLE, null, null, emptyList())

private fun editionOf(licence: () -> LicenceSummary?) =
    object : EditionProvider {
        override fun edition() = PRO

        override fun licence() = licence()
    }

private fun extension(id: String) =
    object : SardExtension {
        override val id = id
        override val displayName = "Name of $id"
    }

/** Rule "Сервер сообщает редакцию, расширения и лицензию вошедшему администратору" (server-core-extensions.feature). */
@MutFlowTest
class ServerInfoControllerTest {
    private fun server(
        licence: () -> LicenceSummary? = { null },
        extensions: List<SardExtension> = emptyList(),
    ): ServerInfo {
        val controller = ServerInfoController(ReportedEdition(PRO), editionOf(licence), ExtensionRegistry(extensions))
        return MutFlow.underTest { controller.server() }
    }

    @Test
    fun `The edition is the one reported at start, the open core has no extensions and no licence`() {
        val info = server()

        assertEquals(EditionInfo("pro", "Pro"), info.edition)
        assertEquals(emptyList(), info.extensions)
        assertNull(info.licence)
    }

    @Test
    fun `The extensions are listed by id with their names`() {
        val info = server(extensions = listOf(extension("sso"), extension("audit")))

        assertEquals(
            listOf(ExtensionInfo("audit", "Name of audit"), ExtensionInfo("sso", "Name of sso")),
            info.extensions,
        )
    }

    @Test
    fun `A licence is shown as the extension reported it`() {
        val expires = Instant.parse("2026-12-31T00:00:00Z")
        val summary =
            LicenceSummary(
                LicenceState.EXPIRED,
                "Pilot LLC",
                expires,
                listOf(LicenceItem("Agents", "10"), LicenceItem("Seats", "3")),
            )

        val licence = server(licence = { summary }).licence

        val items = listOf(LicenceItemInfo("Agents", "10"), LicenceItemInfo("Seats", "3"))
        assertEquals(LicenceInfo(LicenceStatus.EXPIRED, "Pilot LLC", expires, items), licence)
    }

    @Test
    fun `A licence without holder and expiry shows none`() {
        val licence = server(licence = { LicenceSummary(LicenceState.ABSENT) }).licence

        assertEquals(LicenceInfo(LicenceStatus.ABSENT, null, null, emptyList()), licence)
    }

    @Test
    fun `Every state of the licence has the status of the same name`() {
        for (state in LicenceState.entries) {
            var licence: LicenceInfo? = null
            val logs = captureLogs { licence = server(licence = { LicenceSummary(state) }).licence }

            assertEquals(state.name, licence?.state?.name)
            assertTrue(logs.none { "The licence is unavailable" in it }, "$state: $logs")
        }
    }

    @Test
    fun `A licence seam that throws gives the state unavailable and a warning, not a failure`() {
        var info: ServerInfo? = null
        val logs = captureLogs { info = server(licence = { error("licence server down") }) }

        assertEquals(UNAVAILABLE, info?.licence)
        assertEquals(PRO.toInfo(), info?.edition)
        assertTrue(logs.any { "The licence is unavailable: licence server down" in it }, logs.toString())
    }
}
