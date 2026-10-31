// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.events.SettingsEvents
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.springframework.beans.factory.ObjectProvider
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.jdbc.core.JdbcTemplate
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration

/** `sard.logs.*` (S7a, answer 7). */
@ConfigurationProperties("sard.logs")
data class LogProperties(
    val maxLineBytes: Int = MAX_LINE_BYTES,
    val maxStepBytes: Long = MAX_STEP_BYTES,
    val partitionsAheadMonths: Int = PARTITIONS_AHEAD,
    val partitionsCheckInterval: Duration = Duration.ofDays(1),
) {
    fun limits(): LogLimits {
        require(maxLineBytes >= 1) { "sard.logs.max-line-bytes must be at least 1" }
        require(maxStepBytes >= maxLineBytes) { "sard.logs.max-step-bytes must be at least sard.logs.max-line-bytes" }
        return LogLimits(maxLineBytes, maxStepBytes)
    }

    fun monthsAhead(): Int {
        require(partitionsAheadMonths >= 1) { "sard.logs.partitions-ahead-months must be at least 1" }
        return partitionsAheadMonths
    }

    fun checkInterval(): Duration {
        require(partitionsCheckInterval.isPositive) { "sard.logs.partitions-check-interval must be positive" }
        return partitionsCheckInterval
    }
}

/** `sard.run.progress.*` (S7a, answer 8). */
@ConfigurationProperties("sard.run.progress")
data class ProgressProperties(
    val writeInterval: Duration = Duration.ofSeconds(PROGRESS_SECONDS),
) {
    fun interval(): Duration {
        require(writeInterval.isPositive) { "sard.run.progress.write-interval must be positive" }
        return writeInterval
    }
}

/** `sard.run.agent-wait.*`: how long a run waits for an offline agent (J1c, ВJ1). */
@ConfigurationProperties("sard.run.agent-wait")
data class AgentWaitProperties(
    val max: Duration = DEFAULT_AGENT_WAIT_MAX,
) {
    fun limit(): Duration {
        require(max.isPositive) { "sard.run.agent-wait.max must be positive" }
        return max
    }
}

private const val MAX_LINE_BYTES = 8192
private const val MAX_STEP_BYTES = 16L * 1024 * 1024
private const val PARTITIONS_AHEAD = 2
private const val PROGRESS_SECONDS = 5L

/** Steps whose progress the throttle remembers before it drops stale entries. */
private const val THROTTLE_CAPACITY = 10_000

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(LogProperties::class, ProgressProperties::class, AgentWaitProperties::class)
class RunsConfiguration {
    @Bean
    fun sources(
        sessions: TenantSessions,
        clock: Clock,
        settings: SettingsEvents,
    ) = Sources(sessions, clock, UuidV7(clock, SecureRandom()), settings)

    /** Every [RunFinishedListener] bean (S9) hears each finished run. */
    @Bean
    fun runAnnouncer(
        sessions: TenantSessions,
        listeners: ObjectProvider<RunFinishedListener>,
    ) = RunAnnouncer(sessions, RunFinishedPublisher(listeners.orderedStream().toList()))

    @Bean
    fun stepTransitions(
        sessions: TenantSessions,
        clock: Clock,
        announcer: RunAnnouncer,
        events: RunEvents,
    ) = StepTransitions(sessions, clock, announcer, events)

    @Bean
    fun stepResults(
        sessions: TenantSessions,
        clock: Clock,
        transitions: StepTransitions,
        announcer: RunAnnouncer,
    ) = StepResults(sessions, clock, UuidV7(clock, SecureRandom()), transitions, announcer)

    @Bean
    fun stepProgressWrites(
        sessions: TenantSessions,
        transitions: StepTransitions,
    ) = StepProgressWrites(sessions, transitions)

    @Bean
    fun progressThrottle(
        clock: Clock,
        properties: ProgressProperties,
    ) = ProgressThrottle(clock, properties.interval(), THROTTLE_CAPACITY)

    @Bean
    fun stepLogs(
        sessions: TenantSessions,
        clock: Clock,
        properties: LogProperties,
    ) = StepLogs(sessions, clock, properties.limits())

    @Bean
    fun stepLogPartitions(
        jdbc: JdbcTemplate,
        clock: Clock,
        properties: LogProperties,
    ) = StepLogPartitions(jdbc, clock, properties.monthsAhead(), properties.checkInterval())

    @Bean
    fun snapshots(sessions: TenantSessions) = Snapshots(sessions)

    @Bean
    fun stepCounts(sessions: TenantSessions) = StepCounts(sessions)

    @Bean
    fun runs(
        sessions: TenantSessions,
        clock: Clock,
        queued: ObjectProvider<StepsQueued>,
        waiting: AgentWaitProperties,
        presence: ObjectProvider<AgentOnline>,
        events: RunEvents,
    ) = Runs(
        sessions,
        clock,
        UuidV7(clock, SecureRandom()),
        queued.getIfUnique { StepsQueued.NONE },
        waiting.limit(),
        presence.getIfUnique { AgentOnline.ALWAYS },
        events,
    )
}

/** The installation's default step timeouts (J1b, ДJ8). */
@Configuration(proxyBeanMethods = false)
class TimeoutSettingsConfiguration {
    @Bean
    fun timeoutSettings(
        sessions: TenantSessions,
        clock: Clock,
        events: SettingsEvents,
    ) = TimeoutSettings(sessions, clock, events)
}

/** Cancel of a run (J1a): the endpoint's service; the dispatcher, if there is one, sends the agent its CancelStep. */
@Configuration(proxyBeanMethods = false)
class RunCancelsConfiguration {
    @Bean
    fun cancelMarks(
        clock: Clock,
        cancelled: ObjectProvider<StepsCancelled>,
        announcer: RunAnnouncer,
        events: RunEvents,
    ) = CancelMarks(clock, cancelled.getIfUnique { StepsCancelled.NONE }, announcer, events)

    @Bean
    fun runCancels(
        sessions: TenantSessions,
        runs: Runs,
        marks: CancelMarks,
        attempts: ObjectProvider<AttemptCancels>,
    ) = RunCancels(sessions, runs, marks, attempts.getIfUnique { AttemptCancels.NONE })
}

/** Restore from the console (R2a): the service behind the restore endpoints. */
@Configuration(proxyBeanMethods = false)
class RestoresConfiguration {
    @Bean
    fun restores(
        sessions: TenantSessions,
        clock: Clock,
        queued: ObjectProvider<StepsQueued>,
        waiting: AgentWaitProperties,
        presence: ObjectProvider<AgentOnline>,
        events: RunEvents,
    ) = Restores(
        sessions,
        clock,
        UuidV7(clock, SecureRandom()),
        queued.getIfUnique { StepsQueued.NONE },
        waiting.limit(),
        presence.getIfUnique { AgentOnline.ALWAYS },
        events,
    )
}
