// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

/**
 * Something that must hold before the CA directory is touched (ADR 0052). Beans of this type are
 * checked by [PkiAutoConfiguration] before the CA is opened or imported; a check that throws stops the start.
 */
fun interface CaStartPrecondition {
    fun check()
}
