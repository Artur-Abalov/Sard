// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.nio.file.Path

/** Why a CA import is refused (ADR 0052). The order of the first fourteen is the order of the checks. */
enum class CaImportRefusal {
    IMPORT_SOURCE_MISSING,
    IMPORT_FILE_MISSING,
    IMPORT_FILE_UNREADABLE,
    IMPORT_PERMISSIONS_TOO_OPEN,
    CA_CERT_INVALID,
    CA_KEY_INVALID,
    CA_KEY_UNSUPPORTED,
    CA_KEY_MISMATCH,
    CA_NOT_SELF_SIGNED,
    CA_NOT_A_CA,
    CA_KEY_USAGE,
    CA_NOT_YET_VALID,
    CA_EXPIRED,
    IMPORT_WRITE_FAILED,
    CA_ALREADY_PRESENT,
}

/**
 * The server does not start. The message names the reason, SARD_PKI_IMPORT_DIR and the path the reason
 * concerns; it never quotes file content, and no cause is chained, so key material cannot leak through it.
 */
class CaImportRefused(
    val reason: CaImportRefusal,
    detail: String,
    source: Path,
) : IllegalStateException("CA import refused: $reason: $detail (SARD_PKI_IMPORT_DIR=$source)")
