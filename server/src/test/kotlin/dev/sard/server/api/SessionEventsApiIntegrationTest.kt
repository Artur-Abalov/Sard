// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.auth.SessionStore
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.SessionExpired
import dev.sard.server.extension.SignInSucceeded
import dev.sard.server.extension.SignedOut
import dev.sard.server.extension.Subject
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rules "Вход, неудача, блокировка и выход публикуют события" and "Окончание сессии по сроку" (HTTP). */
@RestApiTest
@Import(RecordingConfiguration::class)
class SessionEventsApiIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @Autowired private val log: EventLog,
    @Autowired private val store: SessionStore,
    @Autowired private val clock: MovableClock,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)

    @AfterTest
    fun `back to T0`() {
        clock.now = T0
    }

    @Test
    fun `Успешный вход даёт событие вход удался с адресом клиента и субъектом сессии`() {
        api.signIn()
        log.clear()

        api.signIn()

        val signedIn = log.of<SignInSucceeded>().single()
        assertEquals(Subject.ADMINISTRATOR, signedIn.actor)
        assertTrue(signedIn.clientAddress.isNotBlank())
    }

    @Test
    fun `Выход даёт событие выход и не даёт сессия истекла`() {
        val session = api.signIn()
        log.clear()

        assertEquals(204, api.send("DELETE", "/api/v1/session", session).status)

        assertEquals(1, log.of<SignedOut>().size)
        assertEquals(emptyList(), log.of<SessionExpired>())
    }

    @Test
    fun `Сессия без запросов час и минуту даёт сессия истекла с причиной idle`() {
        val session = api.signIn()
        log.clear()
        clock.now = T0 + Duration.ofMinutes(61)

        store.sweep()

        // The store is shared with the other tests of the context: their sessions run out here too.
        val expired = log.of<SessionExpired>()
        assertTrue(expired.isNotEmpty() && expired.all { it.reason == "idle" && it.actor == Subject.ADMINISTRATOR })
        val count = expired.size
        assertEquals(401, api.get("/api/v1/session", session).status)
        assertEquals(count, log.of<SessionExpired>().size)
    }
}
