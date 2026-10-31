// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

/** The code source a class is loaded from; null for a class that cannot be loaded. */
internal fun locationOf(
    className: String,
    loader: ClassLoader?,
): String? =
    loadedOrNull(className, loader)
        ?.protectionDomain
        ?.codeSource
        ?.location
        ?.toString()

private fun loadedOrNull(
    className: String,
    loader: ClassLoader?,
): Class<*>? = runCatching { Class.forName(className, false, loader) }.getOrNull()
