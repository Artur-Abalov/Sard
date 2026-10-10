// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import java.time.Clock

/**
 * The admin step of the first-start wizard (F4a): whether there is an administrator, and the creation of the
 * first one. The open core keeps the Argon2id hash in its database; an extension that replaces
 * [dev.sard.server.api.SessionApi] signs people in itself and gets [ExternalAdminSetup] (OQ-196, Р16).
 */
interface AdminSetup {
    /** Signing in is managed outside the server. */
    val external: Boolean

    /** The step is done. Reads the database every time. */
    fun done(): Boolean

    /** Sets the first administrator's [password]; false when there is one already. */
    fun create(password: String): Boolean
}

class StoredAdminSetup(
    private val administrators: Administrators,
    private val hasher: PasswordHasher,
    private val clock: Clock,
) : AdminSetup {
    override val external = false

    override fun done(): Boolean = administrators.hash() != null

    override fun create(password: String): Boolean = administrators.create(hasher.hash(password), clock.instant())
}

/** An extension signs people in: the step counts as done, and the server sets no password. */
object ExternalAdminSetup : AdminSetup {
    override val external = true

    override fun done(): Boolean = true

    override fun create(password: String): Boolean = false
}
