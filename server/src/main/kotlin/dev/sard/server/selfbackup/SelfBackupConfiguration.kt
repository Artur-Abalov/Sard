// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.Runs
import dev.sard.server.scheduler.Schedules
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.jdbc.autoconfigure.JdbcConnectionDetails
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import tools.jackson.databind.ObjectMapper
import java.security.SecureRandom
import java.time.Clock
import java.time.ZoneId

/** The self-backup (F6): always on; without a built-in agent it is simply not configured. */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(SelfBackupProperties::class)
class SelfBackupConfiguration {
    /** Settings are checked at start: a schedule that does not parse or a database it cannot find stops the server. */
    @Bean
    fun selfBackupSettings(
        properties: SelfBackupProperties,
        connection: JdbcConnectionDetails,
    ): SelfBackupSettings = properties.settings(connection.jdbcUrl, ZoneId.systemDefault())

    @Bean
    fun selfBackups(
        settings: SelfBackupSettings,
        sessions: TenantSessions,
        clock: Clock,
        schedules: Schedules,
        runs: Runs,
        mapper: ObjectMapper,
    ) = SelfBackups(
        sessions,
        clock,
        UuidV7(clock, SecureRandom()),
        SelfBackupPlan(settings),
        settings.schedule,
        schedules,
        runs,
        mapper,
    )
}
