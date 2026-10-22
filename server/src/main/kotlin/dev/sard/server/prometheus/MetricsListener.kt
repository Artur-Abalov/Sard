// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.io.IOException
import java.net.InetSocketAddress
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

private val log = LoggerFactory.getLogger(MetricsListener::class.java)

private const val WORKERS = 4
private const val NO_BODY = -1L

/**
 * The listener of the metrics (H1 phase 3, the owner's decision 1): a server of its own, on its own address and port,
 * so the console's port 8080 serves no metrics under any path. It is the JDK's `com.sun.net.httpserver` (module
 * jdk.httpserver, in every JRE the image runs on): no dependency is added, no servlet stack, no Spring MVC route that
 * a proxy of the console could reach. It opens when the context starts its lifecycle beans (the web server has an
 * earlier phase of its own, so the order between the two is not promised) and closes with the context; a port it
 * cannot open stops the start.
 */
class MetricsListener(
    private val settings: MetricsSettings,
    scrape: () -> String,
) : SmartLifecycle {
    private val endpoint = MetricsEndpoint(settings.token, scrape)

    @Volatile
    private var server: HttpServer? = null
    private var workers: ExecutorService? = null

    /**
     * The port the listener is bound to: the setting, or the one the OS picked when the settings were built with 0
     * directly (unit tests). `sard.metrics.port=0` is refused by [MetricsProperties].
     */
    val boundPort: Int get() = checkNotNull(server) { "the metrics listener is not running" }.address.port

    override fun start() {
        val pool = Executors.newFixedThreadPool(WORKERS) { Thread(it, "sard-metrics").apply { isDaemon = true } }
        val opened =
            try {
                HttpServer.create(InetSocketAddress(settings.address, settings.port), 0)
            } catch (e: IOException) {
                pool.shutdownNow()
                throw IllegalStateException(
                    "SARD_METRICS_PORT (sard.metrics.port) ${settings.port} cannot be opened on SARD_METRICS_BIND: " +
                        e.javaClass.name,
                    e,
                )
            }
        opened.createContext("/", ::handle)
        opened.executor = pool
        opened.start()
        workers = pool
        server = opened
        log.info("Metrics listener on {}:{}", settings.address.hostAddress, opened.address.port)
    }

    override fun stop() {
        server?.stop(0)
        workers?.shutdownNow()
        server = null
        workers = null
    }

    override fun isRunning() = server != null

    private fun handle(exchange: HttpExchange) {
        exchange.use {
            val reply =
                endpoint.reply(
                    it.requestMethod,
                    it.requestURI.rawPath.orEmpty(),
                    it.requestHeaders.getFirst("Authorization"),
                )
            reply.headers.forEach { (name, value) -> it.responseHeaders.add(name, value) }
            val body = reply.body?.toByteArray(Charsets.UTF_8)
            it.sendResponseHeaders(reply.status, body?.size?.toLong() ?: NO_BODY)
            body?.let { bytes -> it.responseBody.write(bytes) }
        }
    }
}
