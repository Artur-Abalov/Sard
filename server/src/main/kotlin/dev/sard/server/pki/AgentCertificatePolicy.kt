// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.time.Duration
import java.time.format.DateTimeParseException

private const val VALIDITY_SETTING = "sard.pki.agent-certificate-validity"
private const val INTERVAL_SETTING = "sard.pki.agent-renewal-min-interval"
private const val CHECK_SETTING = "sard.pki.agent-certificate-check-interval"
private const val INTERVAL_SHARE = 10L
private const val GREATER_THAN_ZERO = "it must be greater than zero"
private val MIN_VALIDITY: Duration = Duration.ofMinutes(2)

/** A `sard.pki.*` setting the server cannot start with; the message names the setting and its value. */
class InvalidPkiSettingException(
    message: String,
    cause: Throwable? = null,
) : IllegalArgumentException(message, cause)

/**
 * How long an agent certificate lives and how often one may be issued per agent (C1, П1 and П2).
 * Parsed from text so that an unparsable value is refused with the same message as a too small one.
 */
class AgentCertificatePolicy private constructor(
    val validity: Duration,
    val renewalMinInterval: Duration,
    /** How often the server checks the expiry of the agents' certificates (C1 phase 3, П35). */
    val checkInterval: Duration,
) {
    /** The smaller of the setting and a tenth of the validity (Р6): short lifetimes renew in time. */
    val effectiveInterval: Duration
        get() = minOf(renewalMinInterval, validity.dividedBy(INTERVAL_SHARE))

    companion object {
        const val DEFAULT_VALIDITY = "P365D"
        const val DEFAULT_INTERVAL = "PT1H"
        const val DEFAULT_CHECK_INTERVAL = "PT1H"

        fun parse(
            validity: String,
            renewalMinInterval: String,
            checkInterval: String = DEFAULT_CHECK_INTERVAL,
        ): AgentCertificatePolicy {
            val lifetime = duration(VALIDITY_SETTING, validity, "the minimum is $MIN_VALIDITY")
            if (lifetime < MIN_VALIDITY) throw refused(VALIDITY_SETTING, validity, "the minimum is $MIN_VALIDITY")
            return AgentCertificatePolicy(
                lifetime,
                positive(INTERVAL_SETTING, renewalMinInterval),
                positive(CHECK_SETTING, checkInterval),
            )
        }

        private fun positive(
            setting: String,
            value: String,
        ): Duration {
            val interval = duration(setting, value, GREATER_THAN_ZERO)
            if (!interval.isPositive) throw refused(setting, value, GREATER_THAN_ZERO)
            return interval
        }

        private fun duration(
            setting: String,
            value: String,
            rule: String,
        ): Duration =
            try {
                Duration.parse(value)
            } catch (e: DateTimeParseException) {
                throw InvalidPkiSettingException(
                    "$setting: '$value' is not an ISO-8601 duration like PT1H or P365D; $rule",
                    e,
                )
            }

        private fun refused(
            setting: String,
            value: String,
            rule: String,
        ) = InvalidPkiSettingException("$setting: '$value' is refused; $rule")
    }
}
