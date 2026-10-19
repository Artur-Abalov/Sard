// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepState
import dev.sard.server.runs.VerifierVerdict

/**
 * How a verification ended (V-D10); [stored] is restore_verifications.status. «The backup is bad» ([FAILED]) and
 * «the verification did not happen» (the other three non-passes) are different events for different people.
 */
enum class VerificationStatus(
    val stored: String,
) {
    PASSED("passed"),

    /** A stage failed or the fingerprints differ: the backup. */
    FAILED("failed"),

    /** The verifier's environment could not check it. */
    CANNOT_VERIFY("cannot_verify"),

    /** Three attempts in a row ended without a verdict: the verifier vanished or refused. */
    INFRASTRUCTURE_FAILURE("infrastructure_failure"),

    /** No verifier fitted until the job's deadline: the configuration. */
    NO_VERIFIER("no_verifier"),
    ;

    companion object {
        fun of(stored: String): VerificationStatus = entries.single { it.stored == stored }
    }
}

/**
 * Levels of V-D2: a pipeline stops at its first stage that does not pass, and the job reaches the highest level
 * below it. Stages of level 0 (preparation, cleanup) prove no level but stop the pipeline as any other.
 */
object Levels {
    /** The level that proves the data matches the source (4); a passed verdict needs it. */
    const val DATA = 4
    private const val RESTORE_TIME = 5
    private val PROVED = setOf(StageState.PASSED, StageState.NOT_APPLICABLE)

    fun reached(
        stages: List<StepOutput.Stage>,
        restoreMillis: Long?,
    ): Int {
        if (!prepared(stages)) return 0
        val proved = (1..DATA).takeWhile { level -> provedAt(stages, level) }.lastOrNull() ?: 0
        return if (proved == DATA && restoreMillis != null) RESTORE_TIME else proved
    }

    /** No stage of level 0 failed. */
    private fun prepared(stages: List<StepOutput.Stage>): Boolean {
        val preparation = stages.filter { it.level == 0 }
        return preparation.all { it.state in PROVED }
    }

    private fun provedAt(
        stages: List<StepOutput.Stage>,
        level: Int,
    ): Boolean {
        val atLevel = stages.filter { it.level == level }
        return atLevel.isNotEmpty() && atLevel.all { it.state in PROVED }
    }
}

/** What the end of one attempt (one VERIFY step) means for its job. */
sealed interface AttemptOutcome {
    /** The job is done: record it. */
    data class Settled(
        val status: VerificationStatus,
        val level: Int,
        val reason: String?,
    ) : AttemptOutcome

    /** No verdict yet: the job goes back to the queue with [failedAttempts] in a row. */
    data class Retry(
        val failedAttempts: Int,
    ) : AttemptOutcome

    companion object {
        /** The third attempt in a row without a verdict ends the job (design document, «Аренда»). */
        const val ATTEMPTS = 3

        /**
         * The attempt's step ended with [status] and [message]; [verify] is its output, [failedBefore] how many
         * attempts before it ended without a verdict. Only the verifier's verdict says the backup is bad.
         */
        fun of(
            status: StepState,
            message: String?,
            verify: StepOutput.Verify?,
            failedBefore: Int,
        ): AttemptOutcome =
            when (status) {
                StepState.SUCCEEDED, StepState.FAILED -> {
                    verdict(status, message, verify)
                }

                StepState.LOST, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED -> {
                    noVerdict(status, message, failedBefore + 1)
                }

                StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING -> {
                    error("attempt in state ${status.stored} has not ended")
                }
            }

        private fun noVerdict(
            status: StepState,
            message: String?,
            failed: Int,
        ): AttemptOutcome {
            if (failed < ATTEMPTS) return Retry(failed)
            val reason = "$ATTEMPTS attempts without a verdict; the last: ${status.stored}: $message"
            return Settled(VerificationStatus.INFRASTRUCTURE_FAILURE, 0, reason)
        }

        private fun verdict(
            status: StepState,
            message: String?,
            verify: StepOutput.Verify?,
        ): Settled {
            val level = verify?.let { Levels.reached(it.stages, it.restoreMillis) } ?: 0
            return when (verify?.verdict) {
                null -> {
                    Settled(VerificationStatus.CANNOT_VERIFY, level, "no verdict: $message")
                }

                VerifierVerdict.FAILED -> {
                    Settled(VerificationStatus.FAILED, level, message)
                }

                VerifierVerdict.CANNOT_VERIFY -> {
                    Settled(VerificationStatus.CANNOT_VERIFY, level, message)
                }

                VerifierVerdict.PASSED -> {
                    passed(status, level)
                }
            }
        }

        private fun passed(
            status: StepState,
            level: Int,
        ): Settled =
            if (status == StepState.SUCCEEDED && level >= Levels.DATA) {
                Settled(VerificationStatus.PASSED, level, null)
            } else {
                Settled(VerificationStatus.CANNOT_VERIFY, level, "a passed verdict that its stages do not prove")
            }
    }
}
