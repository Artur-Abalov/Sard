// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.security.MessageDigest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNull

private const val CANONICAL =
    """{"facts":[{"name":"bytes","rule":"exact","value":"1024"},""" +
        """{"name":"control:/srv/www","rule":"exact","value":"nonempty"},""" +
        """{"name":"files","rule":"exact","value":"2"},{"name":"sample","rule":"info",""" +
        """"value":"1 of 2 files hashed"},{"name":"sha256:/srv/www/a.txt","rule":"exact","value":"ab"},""" +
        """{"name":"stat:/srv/www/a.txt","rule":"info","value":"5 2026-10-25T03:00:00Z"}],""" +
        """"format":1,"plugin":"files"}"""

private fun sha256(bytes: ByteArray) =
    MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

private const val WHOLE = "a fingerprint without its hash or a hash without its fingerprint"
private const val NOT_ONE = "a fingerprint that is not one"

private val BYTES = CANONICAL.toByteArray()
private val FINGERPRINT = SourceFingerprint(BYTES, sha256(BYTES))

/** The fingerprint of a source (V2) as a backup brings it: its hash is checked, its summary shown. */
@MutFlowTest
class SourceFingerprintTest {
    @Test
    fun `a fingerprint whose hash is that of its bytes has no problem`() {
        assertNull(MutFlow.underTest { FINGERPRINT.problem() })
    }

    @Test
    fun `a hash that is not that of the bytes, not hex or missing is a problem`() {
        val cases =
            mapOf(
                SourceFingerprint(BYTES, sha256("other".toByteArray())) to
                    "a fingerprint whose hash is not that of its bytes",
                SourceFingerprint(BYTES, sha256(BYTES).uppercase()) to
                    "a fingerprint hash that is not 64 lower-case hex digits",
                SourceFingerprint(BYTES, "") to WHOLE,
                SourceFingerprint(ByteArray(0), sha256(BYTES)) to WHOLE,
                SourceFingerprint("[1]".toByteArray(), sha256("[1]".toByteArray())) to NOT_ONE,
                SourceFingerprint("{".toByteArray(), sha256("{".toByteArray())) to NOT_ONE,
            )
        for ((fingerprint, problem) in cases) {
            assertEquals(problem, MutFlow.underTest { fingerprint.problem() }, fingerprint.sha256)
        }
    }

    @Test
    fun `a fingerprint over 1 MiB is a problem`() {
        val big = ByteArray(SourceFingerprint.MAX_BYTES + 1) { 'x'.code.toByte() }

        val problem = MutFlow.underTest { SourceFingerprint(big, sha256(big)).problem() }

        assertEquals("a fingerprint larger than 1 MiB", problem)
    }

    @Test
    fun `the largest fingerprint is no problem for its size`() {
        val big = ByteArray(SourceFingerprint.MAX_BYTES) { 'x'.code.toByte() }

        val problem = MutFlow.underTest { SourceFingerprint(big, sha256(big)).problem() }

        assertEquals(NOT_ONE, problem)
    }

    @Test
    fun `none sent is no fingerprint, an older agent's or a plugin's that takes none`() {
        assertNull(MutFlow.underTest { SourceFingerprint.of(ByteArray(0), "") })
        assertEquals(FINGERPRINT, MutFlow.underTest { SourceFingerprint.of(BYTES, sha256(BYTES)) })
    }

    // The sample names files and their content hashes: the summary shows that there is one, not what it holds.
    @Test
    fun `the summary leaves the hashed files out and counts them`() {
        val summary = MutFlow.underTest { FINGERPRINT.summary() }

        val shown =
            listOf(
                FingerprintFact("bytes", "exact", "1024"),
                FingerprintFact("control:/srv/www", "exact", "nonempty"),
                FingerprintFact("files", "exact", "2"),
                FingerprintFact("sample", "info", "1 of 2 files hashed"),
            )
        assertEquals(FingerprintSummary(sha256(BYTES), 1, "files", shown, hidden = 2), summary)
    }

    @Test
    fun `a summary reads back from its stored fields`() {
        val summary = FINGERPRINT.summary()

        assertEquals(summary, MutFlow.underTest { FingerprintSummary.of(jsonOf(summary.fields())) })
    }

    @Test
    fun `fingerprints are equal by their bytes and hash`() {
        assertEquals(FINGERPRINT, SourceFingerprint(BYTES.copyOf(), sha256(BYTES)))
        assertEquals(FINGERPRINT.hashCode(), SourceFingerprint(BYTES.copyOf(), sha256(BYTES)).hashCode())
        assertNotEquals(FINGERPRINT, SourceFingerprint(BYTES, ""))
        assertNotEquals(FINGERPRINT, SourceFingerprint("{}".toByteArray(), sha256(BYTES)))
        assertNotEquals<Any>(FINGERPRINT, sha256(BYTES))
    }
}

private val MAPPER = tools.jackson.databind.json.JsonMapper.builder().build()

private fun jsonOf(fields: Map<String, Any?>) = MAPPER.readTree(MAPPER.writeValueAsString(fields))
