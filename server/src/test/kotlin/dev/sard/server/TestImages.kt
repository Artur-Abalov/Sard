// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File

/** The images of test/images.env (SC1): a test names an image by its tag and runs the reference pinned by digest. */
object TestImages {
    private val pins: List<String> by lazy {
        val path = requireNotNull(System.getProperty("sard.test.images-file")) { "run the tests with Gradle" }
        File(path)
            .readLines()
            .map { it.trim() }
            .filter { it.isNotEmpty() && !it.startsWith("#") }
            .map { it.substringAfter("=") }
    }

    /** The reference pinned for [tag]; an unpinned tag fails rather than pulling it. */
    fun pinned(tag: String): String {
        val ref = pins.firstOrNull { it.startsWith("$tag@") }
        return ref ?: error("$tag: not in test/images.env")
    }
}
