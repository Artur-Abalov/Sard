// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.SpringApplication
import org.springframework.mock.env.MockEnvironment
import kotlin.test.Test
import kotlin.test.assertEquals

private const val DEFAULT_USER = "org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration"

/** Rule "Цепочка ядра не создаёт сессий контейнера и cookie, кроме своих" (Р34): no default user is made. */
@MutFlowTest
class NoDefaultUserTest {
    private fun excluded(environment: MockEnvironment): List<String> {
        MutFlow.underTest { NoDefaultUser().postProcessEnvironment(environment, SpringApplication()) }
        return environment.getProperty("spring.autoconfigure.exclude").orEmpty().split(',')
    }

    @Test
    fun `the autoconfiguration of the default user is excluded`() {
        assertEquals(listOf(DEFAULT_USER), excluded(MockEnvironment()))
    }

    @Test
    fun `what an operator excludes besides is kept, once`() {
        val environment = MockEnvironment().withProperty("spring.autoconfigure.exclude", "a.B, $DEFAULT_USER ,c.D")

        assertEquals(listOf("a.B", DEFAULT_USER, "c.D"), excluded(environment))
    }
}
