// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import org.springframework.boot.context.properties.ConfigurationProperties

/**
 * `sard.sample.*`: what the sample's seams do, so that a test or a QA run can make each of them behave differently.
 * Every change takes effect on a restart.
 */
@ConfigurationProperties("sard.sample")
data class SampleProperties(
    /** valid, expired, absent, invalid or failing (the licence seam throws). */
    val licence: String = "valid",
    /** Operations the access policy refuses, as "METHOD /path/template" (a comma separated list). */
    val deny: List<String> = emptyList(),
    /** The access policy throws. */
    val policyFailing: Boolean = false,
    /** record, throw or slow (three seconds, then record). */
    val listener: String = "record",
    /** "<kind>:<id>": the subject every request reports; unset leaves the subject source of the core. */
    val subject: String? = null,
    /** Adds GET /sample/stray in a package of the core's namespace, which refuses the start (Р27). */
    val strayController: Boolean = false,
    /** The security customizer declares GET /api/v1/sample/ping public: it answers 204 without a session. */
    val publicPing: Boolean = false,
    /** Declares a SecurityFilterChain of the sample's own, which refuses the start (Р44). */
    val ownChain: Boolean = false,
    /** The security customizer declares GET /sample/open public, outside /api/v1, which refuses the start (Р44). */
    val publicOutside: Boolean = false,
)
