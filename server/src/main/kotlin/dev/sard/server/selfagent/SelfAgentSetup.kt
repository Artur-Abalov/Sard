// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import org.slf4j.LoggerFactory
import java.security.SecureRandom
import java.sql.SQLException
import java.util.HexFormat

private val log = LoggerFactory.getLogger(SelfAgentSetup::class.java)

/** The PostgreSQL role the agent next to the server dumps the database with (migration V202610071200). */
const val SELF_ROLE = "sard_self"
private const val PASSWORD_BYTES = 32

/** Gives a database role a password without the password travelling in SQL text. */
fun interface RolePasswords {
    fun set(
        role: String,
        password: String,
    )
}

/**
 * At every start, with the channel on: the password of [SELF_ROLE] is the one in `db-password`, a new random one
 * when the file is missing or malformed. A role the server cannot change is one WARNING, not a failure: the
 * operator sets it up by hand (docs/operations/self-agent.md). The password is never logged.
 */
class SelfAgentSetup(
    private val channel: SelfChannel,
    private val random: SecureRandom,
    private val passwords: RolePasswords,
) {
    fun run() {
        val password = channel.readPassword() ?: newPassword().also(channel::writePassword)
        try {
            passwords.set(SELF_ROLE, password)
        } catch (e: SQLException) {
            log.warn("role {} not set up (SQLState {}); see docs/operations/self-agent.md", SELF_ROLE, e.sqlState)
        }
    }

    private fun newPassword(): String = HexFormat.of().formatHex(ByteArray(PASSWORD_BYTES).also(random::nextBytes))
}
