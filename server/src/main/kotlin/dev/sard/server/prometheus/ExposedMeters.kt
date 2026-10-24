// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import io.micrometer.prometheusmetrics.PrometheusMeterRegistry

/** The prefixes of the exposed families: Sard's own, the JVM's and the process's (owner's answer OV3). */
private val EXPOSED = listOf("sard_", "jvm_", "process_")
private const val TEXT_004 = "text/plain; version=0.0.4; charset=utf-8"

/**
 * The exposition of the metrics listener (H1 phase 3): only Sard's, the JVM's and the process's meters, so no URI,
 * class of exception or other free text in a label. The other meters (HTTP, gRPC, the pools) stay in the registry and
 * are left out here, at the scrape, not by a meter filter: a filter runs on whatever thread registers a meter first,
 * at any time (the GC's notification thread for a new cause, a gRPC executor for a new method), and in the mutation
 * run such a thread counted the filter as code of whichever test was running (ADR mutflow isolation).
 */
class ExposedMeters(
    private val meters: PrometheusMeterRegistry,
) {
    /** The Prometheus text format 0.0.4, as [PrometheusMeterRegistry.scrape] writes it, of the exposed families. */
    fun text(): String {
        val families = meters.prometheusRegistry.scrape().map { it.metadata.prometheusName }
        return meters.scrape(TEXT_004, families.filter { name -> EXPOSED.any { name.startsWith(it) } }.toSet())
    }
}
