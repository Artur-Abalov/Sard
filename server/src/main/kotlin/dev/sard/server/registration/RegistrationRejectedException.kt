// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

/**
 * Register refused the snapshot; nothing was written. [details] become the ErrorInfo
 * metadata: which field and which limit, never a value from the request.
 */
class RegistrationRejectedException(
    val reason: Reason,
    val details: Map<String, String> = emptyMap(),
    cause: Throwable? = null,
) : RuntimeException("register rejected: ${reason.name} $details", cause) {
    /** The wire reason is [name]: renaming a constant breaks the contract with the agent. */
    enum class Reason {
        PROTOCOL_UNSUPPORTED,
        HOSTNAME_INVALID,
        FIELD_INVALID,
        NAME_INVALID,
        NAME_DUPLICATE,
        SNAPSHOT_TOO_LARGE,
        CONFIG_SCHEMA_INVALID,
        INTERNAL_RETRYABLE,
    }
}
