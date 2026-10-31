// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.dao.DataAccessResourceFailureException
import org.springframework.security.authentication.BadCredentialsException
import org.springframework.security.authentication.TestingAuthenticationToken
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

private const val PASSWORD = "correct-horse-battery"

/** Rule "Вход паролем остаётся контроллером" (Р42): the authentication manager of sign-in over the Argon2 hash. */
@MutFlowTest
class AdministratorAuthenticationTest {
    private val hasher = PasswordHasher()
    private val administrators = FakeAdministrators(hasher.hash(PASSWORD))
    private val login = AdministratorLogin(administrators, hasher)
    private val provider = AdministratorPasswordProvider(administrators, hasher)

    private fun attempt(password: Any) = UsernamePasswordAuthenticationToken.unauthenticated("admin", password)

    @Test
    fun `the right password authenticates the administrator`() {
        val result = MutFlow.underTest { provider.authenticate(attempt(PASSWORD)) }

        assertIs<SubjectAuthentication>(result)
        assertTrue(result.isAuthenticated)
        assertEquals(Subject.ADMINISTRATOR, result.subject)
    }

    @Test
    fun `the login checks the password of the administrator`() {
        assertEquals(PasswordCheck.RIGHT, MutFlow.underTest { login.check(PASSWORD) })
        assertEquals(PasswordCheck.WRONG, MutFlow.underTest { login.check("wrong-password-123") })
    }

    @Test
    fun `a stored value that is no hash is a wrong password`() {
        administrators.stored = "not-a-hash"

        assertEquals(PasswordCheck.WRONG, MutFlow.underTest { login.check(PASSWORD) })
    }

    @Test
    fun `no administrator is a result of its own, not a wrong password`() {
        administrators.stored = null

        assertEquals(PasswordCheck.NO_ADMINISTRATOR, MutFlow.underTest { login.check(PASSWORD) })
        assertFailsWith<NoAdministratorException> { provider.authenticate(attempt(PASSWORD)) }
    }

    @Test
    fun `a database that is gone throws its own exception through the manager`() {
        administrators.down = true

        assertFailsWith<DataAccessResourceFailureException> { MutFlow.underTest { login.check(PASSWORD) } }
    }

    @Test
    fun `an attempt without a password string is bad credentials`() {
        assertFailsWith<BadCredentialsException> { MutFlow.underTest { provider.authenticate(attempt(42)) } }
    }

    @Test
    fun `the provider takes only username and password attempts`() {
        assertTrue(MutFlow.underTest { provider.supports(UsernamePasswordAuthenticationToken::class.java) })
        assertFalse(provider.supports(TestingAuthenticationToken::class.java))
    }
}
