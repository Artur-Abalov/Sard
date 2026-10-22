// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.net.InetAddress
import java.time.Duration

private const val DEFAULT_PORT = 8081
private const val DEFAULT_INTERVAL_SECONDS = 60L
private const val MAX_PORT = 65_535
private const val HTTP_PORT = 8080
private const val GRPC_PORT = 9090
private const val MIN_TOKEN_LENGTH = 32

/** The console's and the agents' ports: the listener never takes them. */
private val RESERVED_PORTS = setOf(HTTP_PORT, GRPC_PORT)

/**
 * `sard.metrics.*` (H1 phase 3), set by SARD_METRICS_BIND, SARD_METRICS_PORT, SARD_METRICS_TOKEN and
 * SARD_METRICS_STATE_INTERVAL. Not a data class: its text form would carry the token.
 */
@ConfigurationProperties("sard.metrics")
class MetricsProperties(
    val bind: String = "127.0.0.1",
    val port: Int = DEFAULT_PORT,
    val token: String = "",
    val stateInterval: Duration = Duration.ofSeconds(DEFAULT_INTERVAL_SECONDS),
) {
    /**
     * The settings, or an [IllegalArgumentException] naming the variable that stops the start. The listener is open
     * to the network only with a token; a token that is only blanks is no token (МР2, owner's answers OV2 and OV7).
     */
    fun settings(): MetricsSettings {
        val address = literal()
        val token = token.takeIf { it.isNotBlank() }
        checkPort()
        checkToken(token)
        checkReachable(address, token)
        require(stateInterval.isPositive) {
            "sard.metrics.state-interval (SARD_METRICS_STATE_INTERVAL) must be positive"
        }
        return MetricsSettings(address, port, token?.let(::BearerToken), stateInterval)
    }

    private fun checkPort() =
        require(port in 1..MAX_PORT && port !in RESERVED_PORTS) {
            "SARD_METRICS_PORT (sard.metrics.port) must be a port from 1 to $MAX_PORT " +
                "other than $HTTP_PORT and $GRPC_PORT"
        }

    private fun checkToken(token: String?) =
        require(token == null || token.length >= MIN_TOKEN_LENGTH) {
            "SARD_METRICS_TOKEN (sard.metrics.token) must be at least $MIN_TOKEN_LENGTH characters"
        }

    private fun checkReachable(
        address: InetAddress,
        token: String?,
    ) = require(token != null || address.isLoopbackAddress) {
        "SARD_METRICS_BIND (sard.metrics.bind) is not a loopback address: the metrics listener opens to the " +
            "network only with a token; set SARD_METRICS_TOKEN or set SARD_METRICS_BIND to 127.0.0.1"
    }

    /** [bind] is an IPv4 or IPv6 literal; a name is never looked up. */
    private fun literal(): InetAddress =
        try {
            InetAddress.ofLiteral(bind)
        } catch (_: IllegalArgumentException) {
            throw IllegalArgumentException("SARD_METRICS_BIND (sard.metrics.bind) must be an IPv4 or IPv6 address")
        }

    override fun toString() = "MetricsProperties(bind=$bind, port=$port, token=***, stateInterval=$stateInterval)"
}

/** [MetricsProperties] after the checks of the start. [token] is null when the listener needs none (loopback). */
class MetricsSettings(
    val address: InetAddress,
    val port: Int,
    val token: BearerToken?,
    val stateInterval: Duration,
) {
    override fun toString() = "MetricsSettings($address:$port, token=$token, every $stateInterval)"
}

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(MetricsProperties::class)
class MetricsSettingsConfiguration {
    @Bean
    fun metricsSettings(properties: MetricsProperties): MetricsSettings = properties.settings()
}
