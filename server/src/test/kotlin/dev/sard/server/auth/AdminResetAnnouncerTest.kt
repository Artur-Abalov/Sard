// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val AT = Instant.parse("2026-10-30T12:00:00Z")
private val TENANT = TenantResolver.DEFAULT_TENANT_ID

/** Rule "Сброс пароля командой admin-reset публикуется один раз при следующем старте" (Р32): the announcing side. */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [AdminResetAnnouncer::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class AdminResetAnnouncerTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val published = mutableListOf<SardEvent>()
    private val announcer =
        AdminResetAnnouncer(jdbc, SardEventPublisher { published += it as SardEvent }, MovableClock(AT))

    @BeforeTest
    fun `no marker`() {
        jdbc.update("delete from admin_password_resets")
    }

    @AfterTest
    fun `forget the marker`() {
        jdbc.update("delete from admin_password_resets")
    }

    private fun mark() = jdbc.update("insert into admin_password_resets (tenant_id) values (?)", TENANT)

    private fun markers() = jdbc.queryForObject("select count(*) from admin_password_resets", Int::class.java)

    // Сценарий: Сброс пароля и старт сервера дают одно событие «настройки изменены» с admin_password
    @Test
    fun `A marker is announced as one admin_password event without an actor or an object, and cleared`() {
        mark()

        MutFlow.underTest { announcer.announce() }

        assertEquals(listOf<SardEvent>(SettingsChanged(AT, TENANT, null, "admin_password", null)), published)
        assertEquals(0, markers())
    }

    // Сценарий: Второй перезапуск после сброса пароля события не даёт
    @Test
    fun `Announcing again publishes nothing more`() {
        mark()
        MutFlow.underTest { announcer.announce() }

        MutFlow.underTest { announcer.announce() }

        assertEquals(1, published.size)
    }

    // Сценарий: Сброс без администратора не даёт события при старте
    @Test
    fun `Without a marker nothing is published`() {
        MutFlow.underTest { announcer.announce() }

        assertEquals(emptyList(), published)
    }

    // Сценарий: Сбой слушателя на событии сброса пароля не повторяет его при перезапуске
    @Test
    fun `A listener that fails does not make the next start announce it again`() {
        val calls = AtomicInteger()
        val failing =
            AdminResetAnnouncer(
                jdbc,
                SardEventPublisher {
                    calls.incrementAndGet()
                    error("listener boom")
                },
                MovableClock(AT),
            )
        mark()

        MutFlow.underTest { failing.announce() }
        MutFlow.underTest { failing.announce() }

        assertEquals(1, calls.get())
        assertEquals(0, markers())
    }
}
