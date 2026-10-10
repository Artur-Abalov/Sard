// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Agents
import dev.sard.server.onboarding.OnboardingSteps
import org.postgresql.PGConnection
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ThreadFactory
import javax.sql.DataSource

private const val SCRAM = "scram-sha-256"

/** A built-in token with less than this left is replaced: the agent must have time to use it (Р1). */
private val EXPIRY_MARGIN: Duration = Duration.ofMinutes(10)

/** The built-in tokens of the tenant the open core pins every caller to. */
private class DefaultTenantTokens(
    private val tokens: EnrollmentTokens,
) : BuiltinTokens {
    override fun usable(secretHash: ByteArray): Boolean {
        val tenant = TenantResolver.DEFAULT_TENANT_ID
        return tokens.builtinUsable(tenant, secretHash, EXPIRY_MARGIN)
    }

    override fun replace(): String = tokens.replaceBuiltin(TenantResolver.DEFAULT_TENANT_ID).reveal()
}

private class DefaultTenantAgents(
    private val agents: Agents,
) : BuiltinAgents {
    override fun live(): Boolean = agents.builtinLive(TenantResolver.DEFAULT_TENANT_ID)
}

/** The role's password goes to the server as a SCRAM verifier computed here, never as text in SQL. */
internal class ScramRolePasswords(
    private val dataSource: DataSource,
) : RolePasswords {
    override fun set(
        role: String,
        password: String,
    ) {
        dataSource.connection.use {
            it.unwrap(PGConnection::class.java).alterUserPassword(role, password.toCharArray(), SCRAM)
        }
    }
}

/**
 * The agent next to the server (docs/specs/server/self-agent.feature): on only when SARD_SELF_DIR is set, and
 * then a channel the server cannot write to, or a bad check interval, stops the server from starting.
 */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(SelfAgentProperties::class)
class SelfAgentConfiguration {
    @Bean
    @ConditionalOnExpression("'\${sard.self-agent.dir:}' != ''")
    fun selfAgentChannel(properties: SelfAgentProperties) = SelfChannel(checkNotNull(properties.settings()).dir)

    @Bean
    @ConditionalOnExpression("'\${sard.self-agent.dir:}' != ''")
    fun selfAgentCheck(
        channel: SelfChannel,
        tokens: EnrollmentTokens,
        agents: Agents,
        steps: OnboardingSteps,
    ) = SelfAgentCheck(channel, DefaultTenantTokens(tokens), DefaultTenantAgents(agents), steps)

    @Bean
    @ConditionalOnExpression("'\${sard.self-agent.dir:}' != ''")
    fun selfAgentLoop(
        properties: SelfAgentProperties,
        channel: SelfChannel,
        check: SelfAgentCheck,
        dataSource: DataSource,
    ): SelfAgentLoop {
        val setup = SelfAgentSetup(channel, SecureRandom(), ScramRolePasswords(dataSource))
        return SelfAgentLoop(checkNotNull(properties.settings()).checkInterval, ::scheduler, setup::run, check::run)
    }

    private fun scheduler(): ScheduledExecutorService {
        val daemon = ThreadFactory { Thread(it, "sard-self-agent").apply { isDaemon = true } }
        return Executors.newSingleThreadScheduledExecutor(daemon)
    }
}
