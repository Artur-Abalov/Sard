// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.api.captureLogs
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SettingsChanged
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.context.ApplicationEvent
import org.springframework.context.ApplicationListener
import org.springframework.context.PayloadApplicationEvent
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private val EVENT = SettingsChanged(Instant.parse("2026-10-30T12:00:00Z"), UUID.randomUUID(), null, "password", null)

private class Ping(
    source: Any,
) : ApplicationEvent(source)

/** Rule "Слушатель не роняет операцию и не лишает событие других слушателей" (Р6, Р21). */
@MutFlowTest
class EventDeliveryTest {
    private val received = mutableListOf<Any>()

    private fun multicaster(vararg listeners: ApplicationListener<*>) =
        SardEventMulticaster().also { m -> listeners.forEach(m::addApplicationListener) }

    private val recording = ApplicationListener<PayloadApplicationEvent<*>> { received += it.payload }
    private val failing = ApplicationListener<PayloadApplicationEvent<*>> { error("listener boom") }

    @Test
    fun `The publisher hands the event to the application`() {
        val published = mutableListOf<Any>()

        MutFlow.underTest { SardEventPublisher { published += it }.publish(EVENT) }

        assertEquals(listOf<Any>(EVENT), published)
    }

    @Test
    fun `A failure of the application is logged with the type of the event and does not reach the caller`() {
        val logs = captureLogs { MutFlow.underTest { SardEventPublisher { error("bus down") }.publish(EVENT) } }

        assertEquals(1, logs.count { "Event settings.changed was not delivered: " in it }, logs.toString())
        assertTrue(logs.any { "bus down" in it }, logs.toString())
    }

    @Test
    fun `A listener that throws on a Sard event is logged and the next listener still gets the event`() {
        val m = multicaster(failing, recording)

        val logs =
            captureLogs { MutFlow.underTest { m.multicastEvent(PayloadApplicationEvent(this, EVENT)) } }

        assertEquals(listOf<Any>(EVENT), received)
        assertEquals(1, logs.count { "A listener failed on the event settings.changed: " in it }, logs.toString())
        assertTrue(logs.any { "listener boom" in it }, logs.toString())
    }

    @Test
    fun `A listener gets a Sard event once`() {
        val m = multicaster(recording)

        MutFlow.underTest { m.multicastEvent(PayloadApplicationEvent(this, EVENT as SardEvent)) }

        assertEquals(listOf<Any>(EVENT), received)
    }

    @Test
    fun `A payload that is not a Sard event keeps the standard behaviour of Spring and fails the publisher`() {
        val m = multicaster(failing)

        val thrown =
            assertFailsWith<IllegalStateException> {
                MutFlow.underTest { m.multicastEvent(PayloadApplicationEvent(this, "plain payload")) }
            }

        assertEquals("listener boom", thrown.message)
    }

    @Test
    fun `An application event that is not a payload keeps the standard behaviour too`() {
        val m = multicaster(ApplicationListener<Ping> { error("ping boom") })

        val thrown = assertFailsWith<IllegalStateException> { MutFlow.underTest { m.multicastEvent(Ping(this)) } }

        assertEquals("ping boom", thrown.message)
    }
}
