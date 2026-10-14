// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/**
 * The scenarios tagged `@unit` of docs/specs/server/server-recovery.feature: how the drill reads the page of
 * docs/operator/12-server-recovery.md. No Docker.
 */
class DrillPageTest {
    private fun block(text: String) = "```bash\n$text\n```"

    @Test
    fun `Разбор страницы находит помеченные блоки по порядку`() {
        val page =
            listOf(
                "# Page",
                "",
                "<!-- drill: first -->",
                block("echo one"),
                "",
                block("echo not marked"),
                "",
                "<!-- drill: second -->",
                block("echo two\necho three"),
            ).joinToString("\n")

        val blocks = DrillPage.blocks(page)

        assertEquals(listOf("first" to "echo one", "second" to "echo two\necho three"), blocks.map { it.id to it.text })
    }

    @Test
    fun `Неизвестная подстановка в блоке останавливает учения до выполнения`() {
        val page = "<!-- drill: one -->\n${block("echo <known>")}\n\n<!-- drill: two -->\n${block("echo <unknown-name>")}\n"
        val executed = mutableListOf<String>()

        val failure =
            assertFailsWith<DrillPageError> {
                DrillPage.run(page, mapOf("known" to "value")) { executed += it.id; 0 }
            }

        assertTrue("two" in failure.message.orEmpty() && "unknown-name" in failure.message.orEmpty(), failure.message)
        assertEquals(emptyList(), executed)
    }

    @Test
    fun `Повтор id блока на странице останавливает учения`() {
        val page = "<!-- drill: same -->\n${block("echo 1")}\n\n<!-- drill: same -->\n${block("echo 2")}\n"

        val failure = assertFailsWith<DrillPageError> { DrillPage.blocks(page) }

        assertTrue("same" in failure.message.orEmpty(), failure.message)
    }

    @Test
    fun `Подстановка заменяет угловые скобки значением, а оболочка с угловыми скобками остаётся как есть`() {
        val page =
            "<!-- drill: shell -->\n" +
                block("cat > f <<'EOF'\nREPO='<адрес-restic>'\nEOF\nif [[ \"\$a\" < \"\$b\" ]]; then echo <name>; fi\ncat <<<\"\$x\" 2>&1 < /dev/null") +
                "\n"
        val prepared = mutableListOf<String>()

        DrillPage.run(page, mapOf("адрес-restic" to "s3:http://h/b", "name" to "N")) { prepared += it.text; 0 }

        assertEquals(
            listOf("cat > f <<'EOF'\nREPO='s3:http://h/b'\nEOF\nif [[ \"\$a\" < \"\$b\" ]]; then echo N; fi\ncat <<<\"\$x\" 2>&1 < /dev/null"),
            prepared,
        )
    }

    @Test
    fun `Блок с ненулевым кодом останавливает учения, остальные не выполняются`() {
        val page = "<!-- drill: a -->\n${block("true")}\n\n<!-- drill: b -->\n${block("false")}\n\n<!-- drill: c -->\n${block("true")}\n"
        val executed = mutableListOf<String>()

        val failure = assertFailsWith<DrillPageError> { DrillPage.run(page, emptyMap()) { executed += it.id; if (it.id == "b") 1 else 0 } }

        assertEquals(listOf("a", "b"), executed)
        assertTrue("b" in failure.message.orEmpty() && "1" in failure.message.orEmpty(), failure.message)
    }

    @Test
    fun `Метка учений без блока bash сразу после неё останавливает учения`() {
        val page = "<!-- drill: a -->\n\n```bash\necho a\n```\n<!-- drill: b -->\n```bash\necho b\n```\n"

        val error = assertFailsWith<DrillPageError> { DrillPage.blocks(page) }

        assertTrue("a" in error.message!!, error.message)
    }
}
