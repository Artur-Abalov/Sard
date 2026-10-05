// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import org.springframework.http.MediaType
import java.nio.charset.StandardCharsets

/** Response headers of a downloaded release file, by its name. */
object AgentPackageHeaders {
    /** Change with every release under the same name: a client asks again each time (ETag answers 304). */
    private val REVALIDATED =
        setOf(AgentPackageCatalog.MANIFEST, AgentPackageCatalog.SUMS, AgentPackageCatalog.SIGNATURE)
    private const val FOREVER = "public, max-age=31536000, immutable"
    private val TEXT = MediaType(MediaType.TEXT_PLAIN, StandardCharsets.UTF_8)
    private val BY_SUFFIX =
        listOf(
            ".deb" to MediaType("application", "vnd.debian.binary-package"),
            ".rpm" to MediaType("application", "x-rpm"),
            ".tar.gz" to MediaType("application", "gzip"),
            ".json" to MediaType.APPLICATION_JSON,
            ".minisig" to TEXT,
        )

    /** Packages carry their version in the name, so their content never changes. */
    fun cacheControl(file: String): String = if (file in REVALIDATED) "no-cache" else FOREVER

    fun mediaType(file: String): MediaType =
        if (file == AgentPackageCatalog.SUMS) {
            TEXT
        } else {
            BY_SUFFIX.firstOrNull { (suffix, _) -> file.endsWith(suffix) }?.second ?: MediaType.APPLICATION_OCTET_STREAM
        }
}
