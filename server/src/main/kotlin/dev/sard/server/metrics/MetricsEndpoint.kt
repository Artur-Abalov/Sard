// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger(MetricsEndpoint::class.java)

private const val OK = 200
private const val UNAUTHORIZED = 401
private const val NOT_FOUND = 404
private const val METHOD_NOT_ALLOWED = 405
private const val SERVER_ERROR = 500
private val CHALLENGE = mapOf("WWW-Authenticate" to "Bearer")
private const val PATH = "/metrics"
private const val METHOD = "GET"
private const val CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"

/** What the listener sends back: a [status], its [headers] and a [body]; null means no body at all. */
data class MetricsReply(
    val status: Int,
    val headers: Map<String, String> = emptyMap(),
    val body: String? = null,
)

/**
 * The whole of what the metrics listener answers (МР3): GET [PATH] gives [scrape], the Prometheus text. With a token,
 * a request without the right one gets 401 before the path and the method are looked at; with the right one, another
 * path is 404 and another method on the path 405. Only 200 has a body, and no reply says why it refused.
 */
class MetricsEndpoint(
    private val token: BearerToken?,
    private val scrape: () -> String,
) {
    fun reply(
        method: String,
        path: String,
        authorization: String?,
    ): MetricsReply =
        when {
            token != null && !token.accepts(authorization) -> MetricsReply(UNAUTHORIZED, CHALLENGE)
            path != PATH -> MetricsReply(NOT_FOUND)
            method != METHOD -> MetricsReply(METHOD_NOT_ALLOWED, mapOf("Allow" to METHOD))
            else -> exposition()
        }

    /** The exposition, or a bare 500: what failed goes to the log by its class and never to the caller. */
    private fun exposition(): MetricsReply =
        runCatching(scrape)
            .map { MetricsReply(OK, mapOf("Content-Type" to CONTENT_TYPE), it) }
            .getOrElse {
                log.warn("Metrics scrape failed with {}", it.javaClass.name)
                MetricsReply(SERVER_ERROR)
            }
}
