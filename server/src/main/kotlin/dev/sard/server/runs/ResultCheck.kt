// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import tools.jackson.databind.json.JsonMapper

private val JSON = JsonMapper.builder().build()
private const val INVALID = "invalid result: "

/** What a finished step produced (proto StepResult.output), stored in run_steps.output. */
sealed interface StepOutput {
    val kind: Action

    /** The JSON of run_steps.output: the fields below, named by [kind]; never the proto's JSON. */
    fun json(): String = JSON.writeValueAsString(mapOf("kind" to kind.stored) + fields())

    fun fields(): Map<String, Any?>

    data class Backup(
        val snapshotId: String,
        val totalBytes: Long,
        val addedBytes: Long,
        /** restic's repository id, which identifies the repository wherever it is reached from. */
        val repositoryId: String,
        /** The result that brought it failed: the snapshot is usable but incomplete (same as the snapshot's). */
        val partial: Boolean = false,
    ) : StepOutput {
        override val kind = Action.BACKUP

        override fun fields() =
            mapOf(
                "snapshotId" to snapshotId,
                "totalBytes" to totalBytes,
                "addedBytes" to addedBytes,
                "repositoryId" to repositoryId,
                "partial" to partial,
            )

        /** Why this output cannot prove a snapshot, or null when it can. */
        fun problem(): String? =
            mapOf(
                "a backup output without a snapshot id" to snapshotId.isEmpty(),
                "a backup output without a repository id" to repositoryId.isEmpty(),
                "a backup output with negative sizes" to (minOf(totalBytes, addedBytes) < 0),
            ).entries.firstOrNull { it.value }?.key
    }

    data class Restore(
        val target: String,
    ) : StepOutput {
        override val kind = Action.RESTORE

        override fun fields() = mapOf("target" to target)
    }

    data class Verify(
        val snapshotId: String,
        val checks: List<Check>,
    ) : StepOutput {
        override val kind = Action.VERIFY

        override fun fields() = mapOf("snapshotId" to snapshotId, "checks" to checks.map { it.fields() })
    }

    data class Check(
        val name: String,
        val passed: Boolean,
        val detail: String,
    ) {
        fun fields() = mapOf("name" to name, "passed" to passed, "detail" to detail)
    }

    data class Run(
        val exitCode: Int,
    ) : StepOutput {
        override val kind = Action.RUN

        override fun fields() = mapOf("exitCode" to exitCode)
    }
}

/** A StepResult as the domain sees it; [status] is null when the agent sent none it knows. */
data class StepReport(
    val status: StepState?,
    val message: String?,
    val output: StepOutput?,
)

/** What is recorded for a report: [invalid] names why the agent's status was replaced by failed. */
data class Verdict(
    val outcome: StepOutcome,
    val output: StepOutput?,
    val invalid: String?,
)

/**
 * Checks a report against the step's action (S7a, answer 5). A success that cannot prove what it
 * claims becomes failed with `invalid result: ...`; a failure keeps its status and loses an output
 * it cannot carry. Either way the result is recorded and acknowledged, so the agent stops sending it.
 */
object ResultCheck {
    fun of(
        action: Action,
        report: StepReport,
    ): Verdict {
        val status = report.status
        if (status == null || status.active || status == StepState.LOST) return invalid("no final status")
        val problem = problem(action, report.output)
        return when {
            problem == null -> Verdict(StepOutcome(status, report.message), marked(report.output, status), invalid = null)
            status == StepState.SUCCEEDED -> invalid(problem)
            else -> Verdict(StepOutcome(status, report.message), null, invalid = null)
        }
    }

    /** A backup output learns whether its result failed; other outputs are as they came. */
    private fun marked(
        output: StepOutput?,
        status: StepState,
    ): StepOutput? = if (output is StepOutput.Backup) output.copy(partial = status != StepState.SUCCEEDED) else output

    private fun problem(
        action: Action,
        output: StepOutput?,
    ): String? =
        when {
            output == null -> if (action == Action.BACKUP) "a succeeded backup without its output" else null
            output.kind != action -> "a ${output.kind.stored} output for a ${action.stored} step"
            output is StepOutput.Backup -> output.problem()
            else -> null
        }

    private fun invalid(problem: String) = Verdict(StepOutcome(StepState.FAILED, INVALID + problem), null, problem)
}
