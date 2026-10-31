// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.extension.SUBJECT_REQUEST_ATTRIBUTE
import dev.sard.server.extension.Subject
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.web.context.request.RequestContextHolder
import org.springframework.web.context.request.ServletRequestAttributes
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val SSO_USER = Subject("sso-user", "alice")

/** Rule "Кто выполняет запрос, политика и события узнают из одного шва SubjectSource" (Р26): the core's source. */
@MutFlowTest
class CoreSubjectSourceTest {
    @AfterTest
    fun `leave no request on the thread`() = RequestContextHolder.resetRequestAttributes()

    private fun inRequest(configure: MockHttpServletRequest.() -> Unit): Subject? {
        RequestContextHolder.setRequestAttributes(ServletRequestAttributes(MockHttpServletRequest().apply(configure)))
        return MutFlow.underTest { CoreSubjectSource().current() }
    }

    private fun MockHttpServletRequest.withSession() {
        val now = Instant.parse("2026-09-01T03:00:00Z")
        setAttribute(SESSION_REQUEST_ATTRIBUTE, AdminSession("s", UUID.randomUUID(), now, now))
    }

    @Test
    fun `the subject of the core session is the administrator`() {
        assertEquals(Subject.ADMINISTRATOR, inRequest { withSession() })
    }

    @Test
    fun `without a core session the subject a replaced filter put on the request`() {
        assertEquals(SSO_USER, inRequest { setAttribute(SUBJECT_REQUEST_ATTRIBUTE, SSO_USER) })
    }

    @Test
    fun `the core session wins over the attribute`() {
        val subject =
            inRequest {
                withSession()
                setAttribute(SUBJECT_REQUEST_ATTRIBUTE, SSO_USER)
            }
        assertEquals(Subject.ADMINISTRATOR, subject)
    }

    @Test
    fun `a request with neither has no subject, nor does one with an attribute of another type`() {
        assertNull(inRequest { })
        assertNull(inRequest { setAttribute(SUBJECT_REQUEST_ATTRIBUTE, "alice") })
    }

    @Test
    fun `outside a request there is no subject`() {
        assertNull(MutFlow.underTest { CoreSubjectSource().current() })
    }
}
