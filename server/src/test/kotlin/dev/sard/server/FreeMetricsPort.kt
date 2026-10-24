// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.metrics.SardMetricNames
import dev.sard.server.metrics.StateGauges
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
 * waits for it. A tick that ran while a mutation test was inside `MutFlow.underTest` counted the code of the state
 * gauges as code of that test, whose mutants then "survived" (ADR mutflow isolation).
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

/**
 * A tick is behind once it failed (counted) or its [StateGauges.publish] returned. The gauge of the tick's time is
 * the first series `publish` registers, the others follow, so its presence only says `publish` began; `publish` is
 * synchronized on the gauges, and taking their monitor waits for it to return.
 */
private fun awaitFirstTick(context: ConfigurableApplicationContext) {
    if (context.getBeanNamesForType(StateLoop::class.java).isEmpty()) return
    val meters = context.getBean(MeterRegistry::class.java)
    val gauges = context.getBean(StateGauges::class.java)
    val deadline = Instant.now().plus(FIRST_TICK)
    while (!published(meters) && !failed(meters)) {
        check(Instant.now().isBefore(deadline)) { "the first state tick did not end within $FIRST_TICK" }
        Thread.sleep(POLL_MILLIS)
    }
    synchronized(gauges) {
        // publish has returned
    }
}

private fun published(meters: MeterRegistry): Boolean {
    val time = meters.find(SardMetricNames.METRICS_STATE_REFRESH_TIMESTAMP).gauge()
    return time != null
}

private fun failed(meters: MeterRegistry): Boolean {
    val failures = meters.find(SardMetricNames.METRICS_STATE_REFRESH_FAILURES).counter()
    return failures != null && failures.count() > 0
}

private const val POLL_MILLIS = 20L
