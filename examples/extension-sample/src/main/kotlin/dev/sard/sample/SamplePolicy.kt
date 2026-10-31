// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.Subject

/** Refuses the operations of `sard.sample.deny`; with `sard.sample.policy-failing` it throws instead of answering. */
class SamplePolicy(
    private val denied: Set<String>,
    private val failing: Boolean,
) : AccessPolicy {
    override fun permits(
        subject: Subject,
        operation: Operation,
    ): Boolean {
        check(!failing) { "the policy store is not reachable" }
        return operation.toString() !in denied
    }
}
