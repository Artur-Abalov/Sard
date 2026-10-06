// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private const val KEY_FILE =
    "untrusted comment: minisign public key DF5D5B6DB257DBFA\n" +
        "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d\n"

/** Rule "Проверка подписи — отдельный шаг с ключом из сборки сервера" (В5). */
@MutFlowTest
class ReleaseKeyTest {
    @Test
    fun `Ключ читается из файла minisign - ID из комментария, строка из второй строки`() {
        val key = MutFlow.underTest { ReleaseKey.parse(KEY_FILE) }
        assertEquals("DF5D5B6DB257DBFA", key.id)
        assertEquals("RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d", key.publicKey)
    }

    @Test
    fun `Файл не в формате minisign не принимается`() {
        val keyLine = "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d\n"
        for (bad in listOf("", keyLine, "untrusted comment: x\nkey\n")) {
            assertFailsWith<IllegalStateException>(bad) { ReleaseKey.parse(bad) }
        }
    }

    @Test
    fun `Ключ в сборке сервера - ключ релизов из deploy release`() {
        assertEquals(ReleaseKey.parse(File("../deploy/release/sard-release.pub").readText()), ReleaseKey.bundled())
    }

    @Test
    fun `releaseKey publicKey совпадает со строкой ключа в README`() {
        val readme = File("../README.md").readText()
        assertEquals(true, "| Public key | `${ReleaseKey.bundled().publicKey}` |" in readme)
        assertEquals(true, "| Key ID | `${ReleaseKey.bundled().id}` |" in readme)
    }
}
