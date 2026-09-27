// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.net.Socket
import java.security.Principal
import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.util.concurrent.atomic.AtomicReference
import javax.net.ssl.SSLEngine
import javax.net.ssl.X509ExtendedKeyManager

/** A server key held in memory; its alias changes with every replacement. */
internal class ServerKey(
    val alias: String,
    val privateKey: PrivateKey,
    val chain: Array<X509Certificate>,
) {
    override fun toString() = "ServerKey($alias)"
}

/**
 * Serves the current server key and can swap it without a restart. The previous key
 * stays resolvable, so a handshake that chose its alias just before a swap completes.
 */
internal class ServerKeyManager(
    initial: ServerKey,
) : X509ExtendedKeyManager() {
    private val keys = AtomicReference(listOf(initial))

    fun replace(key: ServerKey) {
        keys.updateAndGet { listOf(key, it.first()) }
    }

    /** The certificate new handshakes get. */
    fun certificate(): X509Certificate =
        keys
            .get()
            .first()
            .chain
            .first()

    private fun find(alias: String?) = keys.get().firstOrNull { it.alias == alias }

    override fun chooseServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        socket: Socket?,
    ): String? =
        keys
            .get()
            .first()
            .takeIf { it.privateKey.algorithm == keyType }
            ?.alias

    override fun chooseEngineServerAlias(
        keyType: String?,
        issuers: Array<out Principal>?,
        engine: SSLEngine?,
    ): String? = chooseServerAlias(keyType, issuers, null)

    override fun getServerAliases(
        keyType: String?,
        issuers: Array<out Principal>?,
    ): Array<String>? = chooseServerAlias(keyType, issuers, null)?.let { arrayOf(it) }

    override fun getCertificateChain(alias: String?): Array<X509Certificate>? = find(alias)?.chain

    override fun getPrivateKey(alias: String?): PrivateKey? = find(alias)?.privateKey

    /** The server never authenticates as a client. */
    override fun chooseClientAlias(
        keyType: Array<out String>?,
        issuers: Array<out Principal>?,
        socket: Socket?,
    ): String? = null

    override fun getClientAliases(
        keyType: String?,
        issuers: Array<out Principal>?,
    ): Array<String>? = null

    override fun toString() = "ServerKeyManager(${keys.get().first()})"
}
