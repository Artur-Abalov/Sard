// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import org.springframework.core.io.Resource

/** The files of the console (the Vite build) under [root], a directory location ending in a slash. */
class ConsoleBundle(
    private val root: Resource,
) {
    /** True when the bundle has its page: a server built without the console has none (Р3). */
    val isAvailable: Boolean get() = file("index.html") != null

    /**
     * The readable file at [path] relative to the root; a directory or a missing file is null.
     * A path with a percent sign is never a file: a file: or URL resource decodes it once more
     * and `%2e%2e` would step out of the root.
     */
    fun file(path: String): Resource? = if ('%' in path) null else root.createRelative(path).takeIf { it.isReadable }
}
