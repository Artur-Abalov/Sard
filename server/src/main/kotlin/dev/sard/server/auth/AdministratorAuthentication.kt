// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import org.springframework.security.authentication.AuthenticationManager
import org.springframework.security.authentication.AuthenticationProvider
import org.springframework.security.authentication.BadCredentialsException
import org.springframework.security.authentication.ProviderManager
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken
import org.springframework.security.core.Authentication
import org.springframework.security.core.AuthenticationException

/** There is no administrator yet: the first-start wizard has not set the password (F4a, В6). */
class NoAdministratorException : AuthenticationException("There is no administrator yet")

/**
 * Checks the password of the administrator against the Argon2id hash in the database (CORE1 Р42). The hash is read
 * for every check; a database that cannot be reached throws a data access exception, which is no attempt and passes
 * through the manager unchanged (503). A wrong password is a [BadCredentialsException], no administrator a
 * [NoAdministratorException].
 */
class AdministratorPasswordProvider(
    private val administrators: Administrators,
    private val hasher: PasswordHasher,
) : AuthenticationProvider {
    override fun authenticate(authentication: Authentication): Authentication {
        val stored = administrators.hash() ?: throw NoAdministratorException()
        val password = authentication.credentials as? String
        if (password == null || !hasher.matches(password, stored)) throw BadCredentialsException("Wrong password")
        return SubjectAuthentication(Subject.ADMINISTRATOR)
    }

    override fun supports(authentication: Class<*>): Boolean =
        UsernamePasswordAuthenticationToken::class.java.isAssignableFrom(authentication)
}

/** The outcome of checking a sign-in password. */
enum class PasswordCheck { RIGHT, WRONG, NO_ADMINISTRATOR }

/**
 * The authentication manager of sign-in (Р42). It is no bean: Spring Boot would take an `AuthenticationManager` bean
 * for the application's and bring back its default user and form login (Р34). Only [SessionApiImpl] holds it.
 */
class AdministratorLogin(
    administrators: Administrators,
    hasher: PasswordHasher,
) {
    private val manager: AuthenticationManager =
        ProviderManager(AdministratorPasswordProvider(administrators, hasher))

    /** Checks the password of the administrator; a database that is gone throws its own exception (503). */
    fun check(password: String): PasswordCheck =
        try {
            manager.authenticate(UsernamePasswordAuthenticationToken.unauthenticated("admin", password))
            PasswordCheck.RIGHT
        } catch (_: NoAdministratorException) {
            PasswordCheck.NO_ADMINISTRATOR
        } catch (_: BadCredentialsException) {
            PasswordCheck.WRONG
        }
}
