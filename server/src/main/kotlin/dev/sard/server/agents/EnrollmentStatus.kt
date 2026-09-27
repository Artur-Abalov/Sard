// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.enrollment.EnrollmentRejectedException
import dev.sard.server.pki.InvalidCsrException
import io.grpc.Status

/**
 * The one place enrollment errors become gRPC statuses. Interim (S2a): the S2b
 * scenarios decide the final codes and the texts users see.
 */
object EnrollmentStatus {
    fun of(error: Throwable): Status =
        when (error) {
            is EnrollmentRejectedException -> Status.UNAUTHENTICATED.withDescription(error.message)
            is InvalidCsrException -> Status.INVALID_ARGUMENT.withDescription(error.message)
            else -> Status.INTERNAL.withDescription("enrollment failed")
        }
}
