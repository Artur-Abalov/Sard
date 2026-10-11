// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import java.net.URI
import java.util.UUID

/** The URL schemes of a web address; shared by the notification settings. */
internal val WEB_SCHEMES = setOf("http", "https")

/** The language of notifications (`sard.notify.language`, S9b). */
enum class NoticeLanguage(
    val code: String,
) {
    EN("en"),
    RU("ru"),
    ;

    companion object {
        /** The language named by the setting; empty means the default, English. */
        fun of(setting: String): NoticeLanguage {
            val code = setting.trim().ifEmpty { EN.code }
            return requireNotNull(entries.firstOrNull { it.code == code }) {
                "sard.notify.language must be one of ${entries.joinToString { it.code }}, not \"$code\""
            }
        }
    }
}

/** The public address of the console (`sard.console.public-url`, S9b), without trailing slashes. */
class ConsoleUrl private constructor(
    private val base: String,
) {
    fun run(runId: UUID): String = "$base/runs/$runId"

    fun source(sourceId: UUID): String = "$base/sources/$sourceId"

    companion object {
        /** The address, or null when the setting is empty; anything but an http(s) address with a host fails. */
        fun parse(setting: String): ConsoleUrl? =
            setting
                .trim()
                .takeIf { it.isNotEmpty() }
                ?.let { ConsoleUrl(checked(it).trimEnd('/')) }

        private fun checked(address: String): String {
            require(isPlainWebAddress(address)) {
                "sard.console.public-url must be an absolute http or https address with a host, " +
                    "without a query or a fragment, not \"$address\""
            }
            return address
        }

        private fun isPlainWebAddress(address: String): Boolean {
            val uri = runCatching { URI(address) }.getOrNull()
            return uri != null && webAddress(uri) && plain(uri)
        }

        private fun webAddress(uri: URI) = uri.scheme in WEB_SCHEMES && !uri.host.isNullOrEmpty()

        private fun plain(uri: URI) = uri.rawQuery == null && uri.rawFragment == null
    }
}
