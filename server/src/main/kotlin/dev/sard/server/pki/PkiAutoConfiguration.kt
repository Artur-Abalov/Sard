// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.loop.Mode
import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.boot.autoconfigure.ssl.SslBundleRegistrar
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.ssl.SslBundle
import org.springframework.boot.ssl.SslManagerBundle
import org.springframework.context.SmartLifecycle
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import java.nio.file.Path
import java.security.KeyStore
import java.security.SecureRandom
import java.security.cert.CertificateFactory
import java.time.Clock
import java.time.Duration
import javax.net.ssl.KeyManager
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.KeyManagerFactorySpi
import javax.net.ssl.ManagerFactoryParameters
import javax.net.ssl.TrustManagerFactory

/** The bean name of the interval of the agent certificate expiry check. */
const val AGENT_CERTIFICATE_CHECK_INTERVAL = "agentCertificateCheckInterval"

/** The SSL bundle `spring.grpc.server.ssl.bundle` names. */
const val GRPC_SSL_BUNDLE = "sard-grpc"

private val log = LoggerFactory.getLogger(PkiAutoConfiguration::class.java)

/**
 * `sard.pki.*`: where the file CA keeps its key and which names the server certificate carries.
 * [importDir] (SARD_PKI_IMPORT_DIR): a CA to take at the first start, empty means none (ADR 0052).
 * [agentCertificateValidity] and [agentRenewalMinInterval] (SARD_PKI_AGENT_CERTIFICATE_VALIDITY,
 * SARD_PKI_AGENT_RENEWAL_MIN_INTERVAL) and [agentCertificateCheckInterval]
 * (SARD_PKI_AGENT_CERTIFICATE_CHECK_INTERVAL): ISO-8601 durations, text so that [AgentCertificatePolicy] can refuse
 * an unparsable value with the same message as a too small one (C1, П1, П2 and П35).
 */
@ConfigurationProperties("sard.pki")
data class PkiProperties(
    val dir: Path,
    val serverNames: List<String>,
    val renewalCheckInterval: Duration = Duration.ofDays(1),
    val importDir: String = "",
    val agentCertificateValidity: String = AgentCertificatePolicy.DEFAULT_VALIDITY,
    val agentRenewalMinInterval: String = AgentCertificatePolicy.DEFAULT_INTERVAL,
    val agentCertificateCheckInterval: String = AgentCertificatePolicy.DEFAULT_CHECK_INTERVAL,
)

/**
 * The open core's CA and the gRPC listener's TLS (ADR 0014). An enterprise starter
 * replaces the CA by declaring `@AutoConfiguration(before = [PkiAutoConfiguration::class])`.
 */
@AutoConfiguration
@EnableConfigurationProperties(PkiProperties::class)
class PkiAutoConfiguration {
    /** Refuses a setting the server cannot run with, before the CA directory is touched (C1, П1 and П2). */
    @Bean
    fun agentCertificatePolicy(properties: PkiProperties) =
        AgentCertificatePolicy.parse(
            properties.agentCertificateValidity,
            properties.agentRenewalMinInterval,
            properties.agentCertificateCheckInterval,
        )

    /**
     * How often the expiry of the agents' certificates is checked (C1 phase 3, П35), as a plain duration named by the
     * bean, so that the slice that runs the check does not need to know the policy or this package.
     */
    @Bean(AGENT_CERTIFICATE_CHECK_INTERVAL)
    fun agentCertificateCheckInterval(policy: AgentCertificatePolicy): Duration = policy.checkInterval

    @Bean
    @ConditionalOnMissingBean
    @DependsOn("agentCertificatePolicy")
    fun certificateAuthority(
        properties: PkiProperties,
        clock: ObjectProvider<Clock>,
        preconditions: ObjectProvider<CaStartPrecondition>,
        ledger: CaLedger,
        replacements: ObjectProvider<CaReplacementListener>,
    ): CertificateAuthority {
        // The ledger is the database, migrated (F4a, Р19): what the CA directory must agree with. Other slices (the
        // address agents dial, for one) register a CaStartPrecondition; all of them hold
        // before the CA directory is touched, so a start that fails on one leaves no imported CA (ADR 0052).
        preconditions.orderedStream().forEach { it.check() }
        val importDir = properties.importDir.takeIf { it.isNotBlank() }?.let { Path.of(it) }
        val names = properties.serverNames
        val listener =
            CaReplacementListener { previous, current ->
                replacements.orderedStream().forEach { it.replaced(previous, current) }
            }
        val random = SecureRandom()
        val time = clock.getIfAvailable { Clock.systemUTC() }
        return FileCertificateAuthority(properties.dir, names, time, random, ledger, importDir, listener)
    }

    /** Server key from the CA; client certificates, when presented, must chain to it. */
    @Bean
    fun grpcSslBundle(ca: CertificateAuthority) =
        SslBundleRegistrar { registry ->
            val managers = SslManagerBundle.of(FixedKeyManagerFactory(ca.serverKeyManager()), trustOnly(ca))
            registry.registerBundle(GRPC_SSL_BUNDLE, SslBundle.of(null, null, null, null, managers))
        }

    @Bean
    fun serverCertificateRenewal(
        ca: CertificateAuthority,
        properties: PkiProperties,
    ) = ServerCertificateRenewal(ca, properties.renewalCheckInterval)

    private fun trustOnly(ca: CertificateAuthority): TrustManagerFactory {
        val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply { load(null, null) }
        CertificateFactory
            .getInstance("X.509")
            .generateCertificates(ca.caBundlePem().byteInputStream())
            .forEachIndexed { i, certificate -> store.setCertificateEntry("sard-ca-$i", certificate) }
        return TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(store) }
    }
}

/** Hands JSSE the CA's key manager as is, so a renewed server key applies to new handshakes. */
private class FixedKeyManagerFactory(
    keyManager: KeyManager,
) : KeyManagerFactory(FixedSpi(keyManager), null, "sard") {
    private class FixedSpi(
        private val keyManager: KeyManager,
    ) : KeyManagerFactorySpi() {
        override fun engineInit(
            ks: KeyStore?,
            password: CharArray?,
        ) = Unit

        override fun engineInit(spec: ManagerFactoryParameters?) = Unit

        override fun engineGetKeyManagers(): Array<KeyManager> = arrayOf(keyManager)
    }
}

/** Asks the CA to renew the server certificate every [interval]. */
class ServerCertificateRenewal(
    ca: CertificateAuthority,
    interval: Duration,
) : SmartLifecycle by PeriodicTask(
        name = "sard-server-cert-renewal",
        timing = Timing(interval, mode = Mode.FIXED_RATE),
        onFailure = { log.warn("Server certificate renewal failed; retrying at the next check", it) },
        action = { if (ca.renewServerCertificate()) log.info("Server certificate renewed") },
    )
