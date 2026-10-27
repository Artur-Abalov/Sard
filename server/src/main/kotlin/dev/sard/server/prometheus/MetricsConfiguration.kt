// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import dev.sard.server.fleet.AgentPresence
import dev.sard.server.metrics.DatabaseSettingsGauges
import dev.sard.server.metrics.RoleExcessGauges
import dev.sard.server.metrics.RunDurations
import dev.sard.server.metrics.SelfBackupSource
import dev.sard.server.metrics.StateGauges
import dev.sard.server.metrics.StateReader
import dev.sard.server.metrics.StateRefresh
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.springframework.beans.factory.ObjectProvider
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Import
import java.time.Clock

/**
 * Metrics for Prometheus (H1 phase 3), the part that needs no database: the registry that renders them, which
 * meters it carries, and the listener that serves the exposition on a port of its own. The console's port has no
 * route to any of it: the actuator exposes `health` and `info` only (application.yaml), and the listener is not part
 * of the web server. Without this class' settings the listener does not exist, so a start refused for them opens
 * no port.
 *
 * The registry is declared here and not left to Spring Boot's export auto-configuration, which a `@SpringBootTest`
 * switches off: the listener needs the Prometheus text in the tests as in the product.
 */
@Configuration(proxyBeanMethods = false)
@Import(MetricsSettingsConfiguration::class)
class MetricsListenerConfiguration {
    @Bean
    fun prometheusMeterRegistry(): PrometheusMeterRegistry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    /** What the start found about the database (DB1), registered before any migration runs. */
    @Bean
    fun databaseSettingsGauges(meters: PrometheusMeterRegistry) = DatabaseSettingsGauges(meters)

    /** What the start found about the server's database role (DB2), registered before the schema is used. */
    @Bean
    fun roleExcessGauges(meters: PrometheusMeterRegistry) = RoleExcessGauges(meters)

    @Bean
    fun metricsListener(
        settings: MetricsSettings,
        meters: PrometheusMeterRegistry,
    ) = MetricsListener(settings, ExposedMeters(meters)::text)
}

/** The metrics that come from the server's work: the run durations and the state tick (H1 phase 3). */
@Configuration(proxyBeanMethods = false)
@Import(MetricsListenerConfiguration::class)
class MetricsStateConfiguration {
    /** Heard by the announcer of finished runs with the other [dev.sard.server.runs.RunFinishedListener] beans. */
    @Bean
    fun runDurations(meters: PrometheusMeterRegistry) = RunDurations(meters)

    @Bean
    fun stateGauges(meters: PrometheusMeterRegistry) = StateGauges(meters)

    @Bean
    fun stateQueries(sessions: TenantSessions) = StateQueries(sessions)

    /** Presence is asked at the tick; the self-backup comes from the package that knows it ([SelfBackupSource]). */
    @Bean
    fun stateReader(
        queries: StateQueries,
        presence: ObjectProvider<AgentPresence>,
        ca: CertificateAuthority,
        selfBackup: SelfBackupSource,
        clock: Clock,
    ) = StateReader(
        queries::read,
        { presence.getObject().onlineIds() },
        ca::notAfter,
        selfBackup,
        clock,
    )

    @Bean
    fun stateRefresh(
        reader: StateReader,
        gauges: StateGauges,
        clock: Clock,
        meters: PrometheusMeterRegistry,
    ) = StateRefresh(reader::read, gauges, clock, meters)

    @Bean
    fun stateLoop(
        settings: MetricsSettings,
        refresh: StateRefresh,
    ) = StateLoop(settings.stateInterval, tick = refresh::tick)
}
