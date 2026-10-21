// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.boot.test.util.TestPropertyValues
import org.springframework.context.ApplicationContextInitializer
import org.springframework.context.ConfigurableApplicationContext
import java.net.InetAddress
import java.net.ServerSocket

/**
 * The metrics listener of a context (H1 phase 3) opens a port at its start, 8081 by default. A test JVM keeps many
 * contexts alive at once, so each gets a free port of its own on the loopback and a state tick every hour, which the
 * test calls by hand when it needs one. A test that names `sard.metrics.port` or `sard.metrics.state-interval` in its
 * properties (or on a command line of its own) keeps its value.
 */
class FreeMetricsPort : ApplicationContextInitializer<ConfigurableApplicationContext> {
    override fun initialize(context: ConfigurableApplicationContext) {
        val environment = context.environment

        fun named(property: String) =
            listOf("Inlined Test Properties", "commandLineArgs").any {
                environment.propertySources[it]?.containsProperty(property) == true
            }
        if (!named("sard.metrics.port")) {
            val port = ServerSocket(0, 1, InetAddress.ofLiteral("127.0.0.1")).use { it.localPort }
            TestPropertyValues.of("sard.metrics.port=$port").applyTo(environment)
        }
        if (!named("sard.metrics.state-interval")) {
            TestPropertyValues.of("sard.metrics.state-interval=1h").applyTo(environment)
        }
    }
}
