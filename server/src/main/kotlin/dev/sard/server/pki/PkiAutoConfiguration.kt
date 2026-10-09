// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

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
import java.nio.file.Path
import java.security.KeyStore
import java.security.SecureRandom
import java.security.cert.CertificateFactory
import java.time.Clock
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import javax.net.ssl.KeyManager
import javax.net.ssl.KeyManagerFactory
import javax.net.ssl.KeyManagerFactorySpi
import javax.net.ssl.ManagerFactoryParameters
import javax.net.ssl.TrustManagerFactory

/** The SSL bundle `spring.grpc.server.ssl.bundle` names. */
const val GRPC_SSL_BUNDLE = "sard-grpc"

private val log = LoggerFactory.getLogger(PkiAutoConfiguration::class.java)

/**
 * `sard.pki.*`: where the file CA keeps its key and which names the server certificate carries.
 * [importDir] (SARD_PKI_IMPORT_DIR): a CA to take at the first start, empty means none (ADR 0052).
 */
@ConfigurationProperties("sard.pki")
data class PkiProperties(
    val dir: Path,
    val serverNames: List<String>,
    val renewalCheckInterval: Duration = Duration.ofDays(1),
    val importDir: String = "",
)

/**
 * The open core's CA and the gRPC listener's TLS (ADR 0014). An enterprise starter
 * replaces the CA by declaring `@AutoConfiguration(before = [PkiAutoConfiguration::class])`.
 */
@AutoConfiguration
@EnableConfigurationProperties(PkiProperties::class)
class PkiAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean
    fun certificateAuthority(
        properties: PkiProperties,
        preconditions: ObjectProvider<CaStartPrecondition>,
        ledger: CaLedger,
        replacements: ObjectProvider<CaReplacementListener>,
    ): CertificateAuthority {
        // The ledger is the database, migrated (F4a, Р19): what the CA directory must agree with. Other slices (the address agents dial, for one) register a CaStartPrecondition; all of them hold
        // before the CA directory is touched, so a start that fails on one leaves no imported CA (ADR 0052).
        preconditions.orderedStream().forEach { it.check() }
        val importDir = properties.importDir.takeIf { it.isNotBlank() }?.let { Path.of(it) }
        val names = properties.serverNames
        val listener =
            CaReplacementListener { previous, current ->
                replacements.orderedStream().forEach { it.replaced(previous, current) }
            }
        return FileCertificateAuthority(properties.dir, names, Clock.systemUTC(), SecureRandom(), ledger, importDir, listener)
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
    private val ca: CertificateAuthority,
    private val interval: Duration,
) : SmartLifecycle {
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleAtFixedRate(::renew, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    private fun daemon(task: Runnable) = Thread(task, "sard-server-cert-renewal").apply { isDaemon = true }

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun renew() {
        runCatching { ca.renewServerCertificate() }
            .onSuccess { renewed -> if (renewed) log.info("Server certificate renewed") }
            .onFailure { log.warn("Server certificate renewal failed; retrying at the next check", it) }
    }
}
