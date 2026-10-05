// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.notify.telegram.BotToken
import dev.sard.server.notify.telegram.TelegramBotApi
import dev.sard.server.notify.telegram.TelegramChannel
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.RunFinishedListener
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import tools.jackson.databind.ObjectMapper
import java.net.ProxySelector
import java.net.URI
import java.net.http.HttpClient
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.util.concurrent.atomic.AtomicLong

private val log = LoggerFactory.getLogger(NotifyConfiguration::class.java)

private val RETRY_DEFAULTS = RetrySettings()
private const val TOKEN_VARIABLE = "SARD_TELEGRAM_BOT_TOKEN"
private const val CHAT_VARIABLE = "SARD_TELEGRAM_CHAT_ID"
private const val NEEDS_BOTH = "Telegram needs $TOKEN_VARIABLE and $CHAT_VARIABLE;"
private const val NO_CHANNEL = "Notifications are off: no channel (SARD_TELEGRAM_BOT_TOKEN, SARD_TELEGRAM_CHAT_ID)"
private const val TICK_SECONDS = 10L
private const val BATCH = 100
private const val LEASE_MINUTES = 5L
private const val CONNECT_TIMEOUT_SECONDS = 5L
private const val REQUEST_TIMEOUT_SECONDS = 10L

/** `sard.notify.*` (S9a). Credentials are not here: they come from the environment only (OQ-017). */
@ConfigurationProperties("sard.notify")
data class NotifyProperties(
    val tickInterval: Duration = Duration.ofSeconds(TICK_SECONDS),
    val batch: Int = BATCH,
    val lease: Duration = Duration.ofMinutes(LEASE_MINUTES),
    val initialDelay: Duration = RETRY_DEFAULTS.initialDelay,
    val maxDelay: Duration = RETRY_DEFAULTS.maxDelay,
    val maxAttempts: Int = RETRY_DEFAULTS.maxAttempts,
    val maxRetryAfter: Duration = RETRY_DEFAULTS.maxRetryAfter,
    val ttl: Duration = RETRY_DEFAULTS.ttl,
    val telegram: TelegramProperties = TelegramProperties(),
) {
    fun retry() = RetrySettings(initialDelay, maxDelay, maxAttempts, maxRetryAfter, ttl).validated()

    fun queue(): QueueSettings {
        require(batch >= 1) { "sard.notify.batch must be at least 1" }
        require(lease.isPositive) { "sard.notify.lease must be positive" }
        return QueueSettings(batch, lease, retry().ttl)
    }

    fun interval(): Duration {
        require(tickInterval.isPositive) { "sard.notify.tick-interval must be positive" }
        return tickInterval
    }
}

/** `sard.notify.telegram.*`: where the Bot API is and how long to wait for it (answer В8). */
data class TelegramProperties(
    val apiUrl: URI = URI.create("https://api.telegram.org"),
    val connectTimeout: Duration = Duration.ofSeconds(CONNECT_TIMEOUT_SECONDS),
    val requestTimeout: Duration = Duration.ofSeconds(REQUEST_TIMEOUT_SECONDS),
)

/** Telegram credentials from the environment (OQ-017 at stage 1); both empty means no bot. */
data class TelegramCredentials(
    val botToken: String,
    val chatId: String,
) {
    /**
     * The token and chat, or null without a bot (ADR 0024: the server starts, notifications are
     * off). Half a configuration is an error: a token without a chat, or a chat without a token.
     */
    fun configured(): Pair<BotToken, String>? {
        val values = mapOf(TOKEN_VARIABLE to botToken.trim(), CHAT_VARIABLE to chatId.trim())
        val missing = values.filterValues { it.isEmpty() }.keys
        if (missing.size == values.size) return null
        require(missing.isEmpty()) { "$NEEDS_BOTH ${missing.single()} is not set" }
        return BotToken(values.getValue(TOKEN_VARIABLE)) to values.getValue(CHAT_VARIABLE)
    }

    override fun toString() = "TelegramCredentials(botToken=[REDACTED], chatId=$chatId)"
}

/** The Telegram channel, or null without a bot. */
fun telegramChannel(
    credentials: TelegramCredentials,
    properties: TelegramProperties,
    json: ObjectMapper,
): TelegramChannel? {
    val (token, chat) = credentials.configured() ?: return null
    require(properties.apiUrl.scheme in WEB_SCHEMES) { "sard.notify.telegram.api-url must be an http or https URL" }
    require(properties.connectTimeout.isPositive) { "sard.notify.telegram.connect-timeout must be positive" }
    require(properties.requestTimeout.isPositive) { "sard.notify.telegram.request-timeout must be positive" }
    val http =
        HttpClient
            .newBuilder()
            .connectTimeout(properties.connectTimeout)
            .proxy(ProxySelector.getDefault())
            .followRedirects(HttpClient.Redirect.NEVER)
            .build()
    return TelegramChannel(TelegramBotApi(http, properties.apiUrl, token, properties.requestTimeout, json), chat)
}

/**
 * The channels the queue works with. Without a channel it works with none: the server starts,
 * nothing is planned, and the log says why (ADR 0024).
 */
fun activeChannels(channels: List<NotificationChannel>): List<NotificationChannel> =
    if (channels.isEmpty()) {
        emptyList<NotificationChannel>().also { log.warn(NO_CHANNEL) }
    } else {
        channels.also { log.info("Notifications go through {}", channels.map { it.name }) }
    }

/** Micrometer meters of the notification queue. */
class MicrometerNotifyMetrics(
    private val meters: MeterRegistry,
) : NotifyMetrics {
    private val pending = AtomicLong()

    init {
        Gauge.builder("sard.notify.pending", pending) { it.get().toDouble() }.register(meters)
    }

    override fun sent(channel: String) = counter("sard.notify.sent", channel).increment()

    override fun retried(channel: String) = counter("sard.notify.retries", channel).increment()

    override fun undelivered(
        channel: String,
        reason: String,
    ) = Counter
        .builder("sard.notify.undelivered")
        .tag("channel", channel)
        .tag("reason", reason)
        .register(meters)
        .increment()

    override fun pending(count: Long) = pending.set(count)

    private fun counter(
        name: String,
        channel: String,
    ) = Counter.builder(name).tag("channel", channel).register(meters)
}

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(NotifyProperties::class)
class NotifyConfiguration {
    @Bean
    fun telegramCredentials(
        @Value("\${SARD_TELEGRAM_BOT_TOKEN:}") botToken: String,
        @Value("\${SARD_TELEGRAM_CHAT_ID:}") chatId: String,
    ) = TelegramCredentials(botToken, chatId)

    /**
     * The queue works only with a channel; without one it plans nothing, so nothing piles up,
     * and runs finished within the time to live are told about once one exists.
     */
    @Bean
    fun notificationService(
        sessions: TenantSessions,
        clock: Clock,
        properties: NotifyProperties,
        credentials: TelegramCredentials,
        json: ObjectMapper,
        extraChannels: ObjectProvider<NotificationChannel>,
        formatter: NotificationFormatter,
        meters: MeterRegistry,
    ): NotificationService {
        val telegram = telegramChannel(credentials, properties.telegram, json)
        val channels = listOfNotNull(telegram) + extraChannels.orderedStream().toList()
        return NotificationService(
            Deliveries(sessions, UuidV7(clock, SecureRandom())),
            activeChannels(channels),
            formatter,
            RetryPolicy(properties.retry()),
            properties.queue(),
            clock,
            MicrometerNotifyMetrics(meters),
        )
    }

    @Bean
    fun notificationLoop(
        service: NotificationService,
        properties: NotifyProperties,
    ) = NotificationLoop(properties.interval(), service::tick)

    /** A finished run wakes the loop at once; the periodic tick alone already guarantees delivery. */
    @Bean
    fun notificationWakeUp(loop: NotificationLoop) = RunFinishedListener { loop.wake() }
}
