// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.captureLogs
import dev.sard.server.auth.CoreSubjectSourceAutoConfiguration
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import dev.sard.server.extension.TenancyAutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import org.springframework.context.event.EventListener
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class Received {
    val events = CopyOnWriteArrayList<SettingsChanged>()
}

@TestConfiguration(proxyBeanMethods = false)
class ThrowingThenRecording {
    @Bean
    fun received() = Received()

    @Bean
    @org.springframework.core.annotation.Order(1)
    fun first() = Listener { throw IllegalStateException("listener boom") }

    @Bean
    @org.springframework.core.annotation.Order(2)
    fun second(received: Received) = Listener { received.events += it }
}

fun interface Listener {
    @EventListener
    fun on(event: SettingsChanged)
}

/** Rule "Событие доставляется синхронно после фиксации, не более одного раза, и слушатель не роняет операцию". */
class SardEventPublisherTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    EventsAutoConfiguration::class.java,
                    SubjectSourceAutoConfiguration::class.java,
                    CoreSubjectSourceAutoConfiguration::class.java,
                    TenancyAutoConfiguration::class.java,
                    ClockAutoConfiguration::class.java,
                ),
            ).withUserConfiguration(ThrowingThenRecording::class.java)

    private val at = Instant.parse("2026-10-30T12:00:00Z")
    private val event = SettingsChanged(at, UUID.randomUUID(), null, "run_defaults", null)

    @Test
    fun `Исключение одного слушателя не лишает события другого`() {
        runner.run { ctx ->
            val logs = captureLogs { ctx.getBean(SardEventPublisher::class.java).publish(event) }

            assertEquals(listOf(event), ctx.getBean(Received::class.java).events)
            assertEquals(1, logs.count { "listener failed on the event settings.changed" in it }, logs.toString())
        }
    }

    @Test
    fun `Вне запроса актор события не известен`() {
        runner.run { ctx -> assertNull(ctx.getBean(SubjectActors::class.java).current()) }
    }
}
