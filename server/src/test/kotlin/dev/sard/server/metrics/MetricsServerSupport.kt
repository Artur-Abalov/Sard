// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.RestApiTestConfiguration
import dev.sard.server.fleet.AgentPresence
import org.hibernate.resource.jdbc.spi.StatementInspector
import org.springframework.boot.hibernate.autoconfigure.HibernatePropertiesCustomizer
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import java.util.UUID
import java.util.concurrent.ConcurrentLinkedQueue

/**
 * A server on random HTTP and gRPC ports with PostgreSQL and a clock the test moves, whose metrics listener is on a
 * free loopback port (FreeMetricsPort) with the state tick hourly, so a test calls the tick itself. The classes of
 * the metrics tests that need the whole server share this one context.
 */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.RUNTIME)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.agent.stream.check-interval=1h",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.interval=1h",
        "sard.notify.tick-interval=1h",
    ],
)
@Import(TestcontainersConfiguration::class, RestApiTestConfiguration::class, MetricsTestConfiguration::class)
annotation class MetricsServerTest

private const val INSPECTOR = "hibernate.session_factory.statement_inspector"

/** Every SQL statement Hibernate prepares, with the name of the thread that prepared it. */
class StatementLog : StatementInspector {
    private val seen = ConcurrentLinkedQueue<Pair<String, String>>()

    override fun inspect(sql: String): String {
        seen += Thread.currentThread().name to sql
        return sql
    }

    /** The statements prepared on the thread that calls this, while [block] runs. */
    fun <T> hereDuring(block: () -> T): Pair<T, List<String>> {
        val me = Thread.currentThread().name
        val before = seen.size
        val result = block()
        return result to seen.drop(before).filter { it.first == me }.map { it.second }
    }

    /** A point to ask [onThread] about. */
    fun mark(): Int = seen.size

    /** The statements prepared on the threads called [name] since [mark]. */
    fun onThread(
        name: String,
        since: Int,
    ): List<String> = seen.drop(since).filter { it.first == name }.map { it.second }
}

/** The agents that are online, as the test says. */
class FakePresence : AgentPresence {
    @Volatile
    var online: Set<UUID> = emptySet()

    override fun online(agentId: UUID) = agentId in online

    override fun onlineIds(): Set<UUID> = online

    override fun disconnectRevoked(agentId: UUID) {
        online = online - agentId
    }
}

@TestConfiguration(proxyBeanMethods = false)
class MetricsTestConfiguration {
    @Bean
    fun statementLog() = StatementLog()

    @Bean
    fun statementLogging(log: StatementLog) = HibernatePropertiesCustomizer { it[INSPECTOR] = log }

    @Bean
    @Primary
    fun fakePresence() = FakePresence()
}
