// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.enrollment.AgentEndpoint
import java.net.URI

private const val PATH = "/downloads/agent"
private val WEB_SCHEMES = setOf("http", "https")

/**
 * What may stand in an address that goes into a shell command: no quote, space or `$`. A query (`?`) and a
 * fragment (`#`) are not in it either, so the address has neither.
 */
private val SAFE_ADDRESS = Regex("""[A-Za-z0-9._~:/\[\]@%+-]+""")

/**
 * The address the agent packages are fetched from, as it goes into the install commands (U1b, В1):
 * `sard.agent.downloads-url` (SARD_AGENT_DOWNLOADS_URL) when set, else http, the host of the address
 * agents dial and the server's HTTP port. Never taken from the headers of a request.
 */
class DownloadsUrl private constructor(
    private val base: String,
) {
    /** The link of a release file. */
    fun file(name: String): String = "$base$PATH/$name"

    companion object {
        /** A [setting] that is no absolute http or https address with a host, without query and fragment, fails. */
        fun resolve(
            setting: String,
            endpoint: AgentEndpoint,
            httpPort: Int,
        ): DownloadsUrl {
            val address = setting.trim().trimEnd('/')
            return DownloadsUrl(if (address.isEmpty()) "http://${endpoint.host}:$httpPort" else checked(address))
        }

        private fun checked(address: String): String {
            require(isPlainWebAddress(address)) {
                "SARD_AGENT_DOWNLOADS_URL must be an absolute http or https address with a host, " +
                    "without a query or a fragment, not \"$address\""
            }
            return address
        }

        private fun isPlainWebAddress(address: String): Boolean {
            val uri = runCatching { URI(address) }.getOrNull()
            return uri != null && SAFE_ADDRESS.matches(address) && web(uri)
        }

        private fun web(uri: URI) = uri.scheme in WEB_SCHEMES && !uri.host.isNullOrEmpty()
    }
}
