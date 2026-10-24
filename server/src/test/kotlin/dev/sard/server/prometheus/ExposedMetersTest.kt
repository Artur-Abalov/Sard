// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import dev.sard.server.metrics.Exposition
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.Timer
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import kotlin.test.Test
import kotlin.test.assertEquals

/** What the listener's exposition carries: Sard's, the JVM's and the process's meters only (H1 phase 3). */
@MutFlowTest(includeTargets = [ExposedMeters::class])
class ExposedMetersTest {
    private val meters = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    private fun families() = Exposition(MutFlow.underTest { ExposedMeters(meters).text() }).families()

    @Test
    fun `Метры Sard, JVM и процесса есть в экспозиции`() {
        Counter.builder("sard.runs.started").register(meters)
        Counter.builder("jvm.gc.memory.allocated").register(meters)
        Counter.builder("process.files.open").register(meters)

        assertEquals(
            listOf("jvm_gc_memory_allocated_total", "process_files_open_total", "sard_runs_started_total"),
            families().sorted(),
        )
    }

    @Test
    fun `Метры HTTP, gRPC и пулов в реестре есть, в экспозиции нет`() {
        Counter.builder("sard.runs.started").register(meters)
        Timer.builder("http.server.requests").tag("uri", "/api/v1/sources/{id}").register(meters)
        Timer.builder("grpc.server").tag("method", "Connect").register(meters)
        Counter.builder("hikaricp.connections.timeout").register(meters)
        Counter.builder("sardine.eaten").register(meters)

        assertEquals(listOf("sard_runs_started_total"), families())
        assertEquals(5, meters.meters.size)
    }

    @Test
    fun `Пустой реестр - пустая экспозиция`() {
        assertEquals(emptyList(), families())
    }
}
