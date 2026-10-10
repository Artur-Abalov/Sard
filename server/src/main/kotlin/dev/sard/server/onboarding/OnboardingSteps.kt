// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant

private const val CA_STEP = "ca"

/** The steps of the wizard that are recorded, installation-wide. */
interface OnboardingSteps {
    fun caConfirmed(): Boolean

    /** True when this call confirmed the step, false when it was confirmed already. */
    fun confirmCa(now: Instant): Boolean
}

class JdbcOnboardingSteps(
    private val jdbc: JdbcTemplate,
) : OnboardingSteps {
    override fun caConfirmed(): Boolean =
        jdbc.queryForObject("select count(*) from onboarding_steps where step = ?", Int::class.java, CA_STEP) == 1

    override fun confirmCa(now: Instant): Boolean =
        jdbc.update(
            "insert into onboarding_steps (step, completed_at) values (?, ?) on conflict do nothing",
            CA_STEP,
            Timestamp.from(now),
        ) == 1
}
