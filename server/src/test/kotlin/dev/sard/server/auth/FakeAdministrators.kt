// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.dao.DataAccessResourceFailureException
import java.time.Instant

/** The administrator row in memory; [down] makes every read and write fail as a database that is gone would. */
class FakeAdministrators(
    var stored: String? = null,
    var down: Boolean = false,
) : Administrators {
    var reads = 0

    private fun alive() {
        if (down) throw DataAccessResourceFailureException("database is down")
    }

    override fun hash(): String? {
        alive()
        reads++
        return stored
    }

    override fun create(
        hash: String,
        now: Instant,
    ): Boolean {
        alive()
        if (stored != null) return false
        stored = hash
        return true
    }

    override fun replaceHash(
        expected: String,
        hash: String,
        now: Instant,
    ): Boolean {
        alive()
        if (stored != expected) return false
        stored = hash
        return true
    }

    override fun remove(): Boolean {
        alive()
        val had = stored != null
        stored = null
        return had
    }
}
