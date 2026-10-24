// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.metrics.SardMetricNames
import dev.sard.server.prometheus.StateLoop
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.boot.context.event.ApplicationReadyEvent
import org.springframework.boot.test.util.TestPropertyValues
import org.springframework.context.ApplicationContextInitializer
import org.springframework.context.ApplicationListener
import org.springframework.context.ConfigurableApplicationContext
import java.net.InetAddress
import java.time.Duration
import java.time.Instant

/** How long a start waits for its first state tick: a few queries of an idle database. */
private val FIRST_TICK = Duration.ofSeconds(60)

/**
 * The metrics listener of a context (H1 phase 3) opens a port at its start, 8081 by default. A test JVM keeps many
 * contexts alive at once, so each gets a free port of its own on the loopback and a state tick every hour, which the
 * test calls by hand when it needs one. A test that names `sard.metrics.port` or `sard.metrics.state-interval` in its
 * properties (or on a command line of its own) keeps its value.
 *
 * The first tick runs at the start, in the background, and registers a gauge for every series it finds; the start
 * waits for it. A registration runs the meter filter, and one that ran while a mutation test was inside
 * `MutFlow.underTest` counted the filter as code of that test, whose mutants then "survived" (ADR mutflow isolation).
 * With the tick hourly and the first one behind, no tick runs inside a test.
 */
class FreeMetricsPort : ApplicationContextInitializer<ConfigurableApplicationContext> {
    override fun initialize(context: ConfigurableApplicationContext) {
        val environment = context.environment

        fun named(property: String) =
            listOf("Inlined Test Properties", "commandLineArgs").any {
                environment.propertySources[it]?.containsProperty(property) == true
            }
        if (!named("sard.metrics.port")) {
            val port = TestPorts.reserve(InetAddress.ofLiteral("127.0.0.1"))
            TestPropertyValues.of("sard.metrics.port=$port").applyTo(environment)
        }
        if (!named("sard.metrics.state-interval")) {
            TestPropertyValues.of("sard.metrics.state-interval=1h").applyTo(environment)
        }
        context.addApplicationListener(
            ApplicationListener<ApplicationReadyEvent> { awaitFirstTick(it.applicationContext) },
        )
    }
}

/** A tick is behind once it refreshed the state (the gauge of its time exists) or failed (counted). */
private fun awaitFirstTick(context: ConfigurableApplicationContext) {
    if (context.getBeanNamesForType(StateLoop::class.java).isEmpty()) return
    val meters = context.getBean(MeterRegistry::class.java)
    val deadline = Instant.now().plus(FIRST_TICK)
    while (Instant.now().isBefore(deadline)) {
        val refreshed = meters.find(SardMetricNames.METRICS_STATE_REFRESH_TIMESTAMP).gauge() != null
        val failed = (meters.find(SardMetricNames.METRICS_STATE_REFRESH_FAILURES).counter()?.count() ?: 0.0) > 0
        if (refreshed || failed) return
        Thread.sleep(POLL_MILLIS)
    }
    error("the first state tick did not end within $FIRST_TICK")
}

private const val POLL_MILLIS = 20L
