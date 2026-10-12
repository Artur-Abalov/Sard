// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.OnboardingApi
import dev.sard.server.api.SessionApi
import dev.sard.server.auth.AdminSetup
import dev.sard.server.auth.Administrators
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.PasswordHasher
import dev.sard.server.auth.SessionStore
import dev.sard.server.auth.sha256
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.lang.reflect.Modifier
import java.util.IdentityHashMap
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val MAX_DEPTH = 8

/** What is reachable from some roots: the strings and the byte arrays among it. */
private class Reached {
    val strings = mutableListOf<String>()
    val bytes = mutableListOf<ByteArray>()
    private val seen = IdentityHashMap<Any, Boolean>()

    /** Walks [value] through fields of the server's classes, collections and maps, [depth] levels at most. */
    fun walk(
        value: Any?,
        depth: Int = 0,
    ) {
        if (value == null || depth > MAX_DEPTH || seen.put(value, true) != null) return
        if (!keep(value)) children(value).forEach { walk(it, depth + 1) }
    }

    /** Strings and byte arrays are what is looked for; true when [value] was one of them. */
    private fun keep(value: Any): Boolean {
        when (value) {
            is CharSequence -> strings += value.toString()
            is CharArray -> strings += String(value)
            is ByteArray -> bytes += value
            else -> return false
        }
        return true
    }

    private fun children(value: Any): List<Any?> =
        when (value) {
            is Array<*> -> value.toList()
            is Map<*, *> -> value.entries.flatMap { listOf(it.key, it.value) }
            is Iterable<*> -> value.toList()
            is java.util.concurrent.atomic.AtomicReference<*> -> listOf(value.get())
            else -> ownFields(value)
        }

    private fun ownFields(value: Any): List<Any?> {
        val own = value.javaClass.name.startsWith("dev.sard.server")
        return if (own) fieldsOf(value.javaClass).map { it.get(value) } else emptyList()
    }
}

/** Every string and byte array reachable from [roots]. */
private fun reachable(vararg roots: Any): Pair<List<String>, List<ByteArray>> {
    val reached = Reached()
    roots.forEach { reached.walk(it) }
    return reached.strings to reached.bytes
}

private fun fieldsOf(type: Class<*>) =
    generateSequence<Class<*>>(type) { it.superclass }
        .takeWhile { it.name.startsWith("dev.sard.server") }
        .flatMap { it.declaredFields.asSequence() }
        .filterNot { Modifier.isStatic(it.modifiers) }
        .onEach { it.isAccessible = true }
        .toList()

/**
 * Scenarios "После выдачи кода объекты сервера не содержат сам код" and "После шага admin объекты сервера не
 * содержат пароль и его хэш" (@service) of docs/specs/server/onboarding-setup.feature: the beans of the server
 * that deal with the code, the sessions and the password are walked field by field.
 */
@FirstStartTest
class NoSecretsInBeansIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val codes: SetupCodes,
    @Autowired private val sessions: SetupSessions,
    @Autowired private val codeAttempts: SetupCodeAttempts,
    @Autowired private val adminSessions: SessionStore,
    @Autowired private val signInAttempts: LoginAttemptTracker,
    @Autowired private val sessionApi: SessionApi,
    @Autowired private val onboardingApi: OnboardingApi,
    @Autowired private val adminSetup: AdminSetup,
    @Autowired private val administrators: Administrators,
    @Autowired private val hasher: PasswordHasher,
    @Autowired private val announcer: SetupCodeAnnouncer,
    @Autowired private val installation: CleanInstallation,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = FirstStartClient(port, mapper)

    @BeforeTest
    fun `a clean installation with the code C`() = installation.restore()

    private fun beans() =
        reachable(
            codes,
            sessions,
            codeAttempts,
            adminSessions,
            signInAttempts,
            sessionApi,
            onboardingApi,
            adminSetup,
            administrators,
            hasher,
            announcer,
        )

    @Test
    fun `После выдачи кода объекты сервера не содержат сам код`() {
        val (strings, bytes) = beans()

        for (form in listOf(CODE, CODE.replace("-", ""), CODE.lowercase(), CODE.replace("-", "").lowercase())) {
            assertTrue(strings.none { form in it }, "a string holds the code")
            val ascii = form.toByteArray(Charsets.US_ASCII)
            assertTrue(bytes.none { it.contentEquals(ascii) }, "a byte array holds the code")
        }
        val digest = sha256(CODE.replace("-", ""))
        assertTrue(bytes.any { it.contentEquals(digest) }, "no field holds the SHA-256 of the normalised code")
    }

    @Test
    fun `После шага admin объекты сервера не содержат пароль и его хэш`() {
        client.completeWizard(OWNER_PASSWORD)
        assertEquals(204, client.login(OWNER_PASSWORD).status)
        val stored = jdbc.queryForObject("select password_hash from administrators", String::class.java)!!

        val (strings, bytes) = beans()

        assertTrue(strings.none { OWNER_PASSWORD in it }, "a string holds the password")
        val passwordBytes = OWNER_PASSWORD.toByteArray(Charsets.UTF_8)
        assertTrue(bytes.none { it.contentEquals(passwordBytes) }, "a byte array holds the password")
        assertTrue(strings.none { stored in it }, "a string holds the hash")
        assertTrue(bytes.none { it.contentEquals(stored.toByteArray(Charsets.UTF_8)) }, "a byte array holds the hash")
        assertFalse(strings.isEmpty(), "the walk found nothing")
    }
}
