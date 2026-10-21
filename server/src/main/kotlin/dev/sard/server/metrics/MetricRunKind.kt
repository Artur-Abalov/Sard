// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.runs.RunKind
import dev.sard.server.runs.Trigger

/**
 * What a run is for the metrics (МР9): a verification is told apart by its trigger and not by its kind, so a run of
 * the trigger verification is [VERIFY] whatever else it is; otherwise it is a backup or a restore. [tag] is the value
 * of the label `kind`.
 */
enum class MetricRunKind(
    val tag: String,
) {
    BACKUP("backup"),
    RESTORE("restore"),
    VERIFY("verify"),
    ;

    companion object {
        fun of(
            trigger: Trigger,
            kind: RunKind,
        ): MetricRunKind =
            when {
                trigger == Trigger.VERIFICATION -> VERIFY
                kind == RunKind.RESTORE -> RESTORE
                else -> BACKUP
            }

        /** The value of the label `kind` as the state query writes it. */
        fun ofTag(tag: String): MetricRunKind = entries.single { it.tag == tag }
    }
}
