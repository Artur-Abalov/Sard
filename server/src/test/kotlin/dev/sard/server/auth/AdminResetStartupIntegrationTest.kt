// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.TenantResolver
import org.springframework.beans.factory.InitializingBean
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** A reset the command left in the database before the server started (Р32). */
@TestConfiguration(proxyBeanMethods = false)
class ResetBeforeStart {
    @Bean
    @DependsOn("flywayInitializer")
    fun resetBeforeStart(jdbc: JdbcTemplate) =
        InitializingBean {
            jdbc.update("insert into admin_password_resets (tenant_id) values (?)", TenantResolver.DEFAULT_TENANT_ID)
        }
}

/** Rule "Сброс пароля командой admin-reset публикуется один раз при следующем старте" (Р32): the start. */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class, RecordingConfiguration::class, ResetBeforeStart::class)
class AdminResetStartupIntegrationTest(
    @Autowired private val log: EventLog,
    @Autowired private val jdbc: JdbcTemplate,
) {
    // Сценарий: Сброс пароля и старт сервера дают одно событие «настройки изменены» с admin_password
    @Test
    fun `The start that follows a reset publishes one admin_password event to the listeners and clears the marker`() {
        val heard = log.settings("admin_password")

        assertEquals(1, heard.size, "${log.events}")
        assertNull(heard.single().actor)
        assertNull(heard.single().objectId)
        assertEquals(TenantResolver.DEFAULT_TENANT_ID, heard.single().tenantId)
        assertEquals(listOf(heard.single()), log.of<SettingsChanged>())
        assertEquals(0, jdbc.queryForObject("select count(*) from admin_password_resets", Int::class.java))
    }
}
