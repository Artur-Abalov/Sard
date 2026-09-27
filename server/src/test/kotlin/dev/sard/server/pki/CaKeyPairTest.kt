// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.rootLike
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.Base64
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse

/** What a loaded CA must be before it is trusted: self-signed, a CA, holding its key. */
@MutFlowTest
class CaKeyPairTest {
    @Test
    fun `a generated root is accepted`() {
        val keys = Keys.generate(random())
        val root = Certificates.root(keys, NOW, random())
        assertEquals(root, MutFlow.underTest { CaKeyPair(root, keys.private) }.certificate)
    }

    @Test
    fun `a certificate not signed by its own key is refused`() {
        val keys = Keys.generate(random())
        val forged = rootLike(keys, Keys.generate(random()).private, ca = true)
        val e = assertFailsWith<IllegalStateException> { MutFlow.underTest { CaKeyPair(forged, keys.private) } }
        assertEquals("CA certificate CN=Sard CA is not self-signed", e.message)
    }

    @Test
    fun `a certificate without the CA flag is refused`() {
        val keys = Keys.generate(random())
        val leaf = rootLike(keys, keys.private, ca = false)
        val e = assertFailsWith<IllegalStateException> { MutFlow.underTest { CaKeyPair(leaf, keys.private) } }
        assertEquals("CA certificate CN=Sard CA is not a CA", e.message)
    }

    @Test
    fun `a key of another pair is refused`() {
        val keys = Keys.generate(random())
        val root = rootLike(keys, keys.private, ca = true)
        val otherKey = Keys.generate(random()).private
        val e = assertFailsWith<IllegalStateException> { MutFlow.underTest { CaKeyPair(root, otherKey) } }
        assertEquals("CA key does not match the CA certificate CN=Sard CA", e.message)
    }

    @Test
    fun `the key never shows in toString`() {
        val keys = Keys.generate(random())
        val pair = CaKeyPair(Certificates.root(keys, NOW, random()), keys.private)
        assertFalse(Base64.getEncoder().encodeToString(keys.private.encoded) in pair.toString())
        assertEquals("CaKeyPair(CN=Sard CA)", pair.toString())
    }
}
