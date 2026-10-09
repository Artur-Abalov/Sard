// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.enrollment.EnrollmentToken
import dev.sard.server.enrollment.MalformedEnrollmentTokenException
import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger(SelfAgentCheck::class.java)

/** The built-in enrollment tokens of the default tenant, as the check needs them. */
interface BuiltinTokens {
    /** Whether a built-in token with this secret hash is unused, not revoked and not about to expire. */
    fun usable(secretHash: ByteArray): Boolean

    /** Revokes the active built-in tokens, issues a new one and returns its string. */
    fun replace(): String
}

/** The built-in agents of the default tenant. */
fun interface BuiltinAgents {
    /** Whether there is a built-in agent that is not revoked, online or not. */
    fun live(): Boolean
}

/**
 * One pass of the periodic check (docs/specs/server/self-agent.feature, rule "Проверка выпускает встроенный
 * токен, только пока нет живого встроенного агента"): with a live built-in agent the token file goes; without one
 * the file holds a usable built-in token, issued here when it does not. Passes never overlap. A failure
 * changes nothing in the channel, is logged once without the token and is left for the next pass.
 */
class SelfAgentCheck(
    private val channel: SelfChannel,
    private val tokens: BuiltinTokens,
    private val agents: BuiltinAgents,
) {
    private val lock = Any()

    fun run() {
        synchronized(lock) {
            runCatching(::pass).onFailure(::warn)
        }
    }

    private fun warn(failure: Throwable) {
        log.warn("built-in agent check failed ({}); retrying at the next one", failure.javaClass.simpleName)
    }

    private fun pass() {
        if (agents.live()) {
            channel.deleteToken()
            return
        }
        val hash = channel.readToken()?.let(::secretHashOf)
        if (hash != null && tokens.usable(hash)) return
        channel.writeToken(tokens.replace())
        log.info("built-in agent enrollment token written to {}", channel.tokenFile)
    }

    private fun secretHashOf(text: String): ByteArray? =
        try {
            EnrollmentToken.parse(text).secret.hash()
        } catch (_: MalformedEnrollmentTokenException) {
            null
        }
}
