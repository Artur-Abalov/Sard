// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant

private const val CA_STEP = "ca"
private const val KEYS_STEP = "keys_confirmed"
private const val UPGRADED_MARK = "upgraded"

/** The steps of the wizard that are recorded, installation-wide. */
interface OnboardingSteps {
    fun caConfirmed(): Boolean

    /** True when this call confirmed the step, false when it was confirmed already. */
    fun confirmCa(now: Instant): Boolean

    /** The step keys_confirmed is done: the onboarding is complete (F4b). */
    fun keysConfirmed(): Boolean

    /** True when this call completed the onboarding, false when it was complete already. */
    fun confirmKeys(now: Instant): Boolean

    /** The installation had agents or sources of the administrator when it first started with F4b (Р21). */
    fun upgraded(): Boolean
}

class JdbcOnboardingSteps(
    private val jdbc: JdbcTemplate,
) : OnboardingSteps {
    override fun caConfirmed(): Boolean = recorded(CA_STEP)

    override fun confirmCa(now: Instant): Boolean = record(CA_STEP, now)

    override fun keysConfirmed(): Boolean = recorded(KEYS_STEP)

    override fun confirmKeys(now: Instant): Boolean = record(KEYS_STEP, now)

    override fun upgraded(): Boolean =
        jdbc.queryForObject("select count(*) from onboarding_marks where mark = ?", Int::class.java, UPGRADED_MARK) == 1

    private fun recorded(step: String): Boolean =
        jdbc.queryForObject("select count(*) from onboarding_steps where step = ?", Int::class.java, step) == 1

    private fun record(
        step: String,
        now: Instant,
    ): Boolean =
        jdbc.update(
            "insert into onboarding_steps (step, completed_at) values (?, ?) on conflict do nothing",
            step,
            Timestamp.from(now),
        ) == 1
}
