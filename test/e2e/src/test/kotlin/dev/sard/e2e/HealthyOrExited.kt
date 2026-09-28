// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.ContainerLaunchException
import org.testcontainers.containers.wait.strategy.AbstractWaitStrategy
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.time.Instant

/**
 * Waits until `GET [path]` on [port] answers 200, and fails as soon as the
 * container exits. Testcontainers' HTTP wait keeps polling a dead container
 * until its timeout: a server that refuses its configuration exits in seconds
 * but would hold the test for the whole startup timeout.
 */
internal class HealthyOrExited(
    private val path: String,
    private val port: Int,
) : AbstractWaitStrategy() {
    private val client = HttpClient.newBuilder().connectTimeout(POLL).build()

    override fun waitUntilReady() {
        val url = URI.create("http://${waitStrategyTarget.host}:${waitStrategyTarget.getMappedPort(port)}$path")
        val deadline = Instant.now() + startupTimeout
        while (Instant.now() < deadline) {
            if (!waitStrategyTarget.isRunning) throw ContainerLaunchException("container exited before $path answered 200")
            if (answers200(url)) return
            Thread.sleep(POLL.toMillis())
        }
        throw ContainerLaunchException("$path did not answer 200 within $startupTimeout")
    }

    private fun answers200(url: URI): Boolean =
        runCatching {
            client.send(HttpRequest.newBuilder(url).timeout(POLL).build(), HttpResponse.BodyHandlers.discarding()).statusCode()
        }.getOrNull() == 200

    private companion object {
        val POLL: Duration = Duration.ofMillis(500)
    }
}
