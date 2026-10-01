// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import dev.sard.server.notify.Message
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Telegram HTML (S9a, test strategy 4): user text is escaped, and the message fits Telegram's limit. */
class TelegramHtmlTest {
    /** The text Telegram shows: tags dropped, entities decoded, as it counts the limit. */
    private fun visible(html: String) =
        html
            .replace(Regex("</?(b|code)>"), "")
            .replace("&lt;", "<")
            .replace("&gt;", ">")
            .replace("&amp;", "&")

    @Test
    fun `parts become text, bold and code`() {
        val html = TelegramHtml.render(Message(Message.Bold("Backup failed"), Message.Text(": "), Message.Code("db1")))
        assertEquals("<b>Backup failed</b>: <code>db1</code>", html)
    }

    @Test
    fun `markup in user text is escaped in every part`() {
        val evil = "<a href=\"x\">&amp;</a>"
        val html = TelegramHtml.render(Message(Message.Text(evil), Message.Bold(evil), Message.Code(evil)))
        val escaped = "&lt;a href=\"x\"&gt;&amp;amp;&lt;/a&gt;"
        assertEquals("$escaped<b>$escaped</b><code>$escaped</code>", html)
    }

    @Test
    fun `a message within the limit is kept whole`() {
        val text = "é".repeat(TELEGRAM_MAX_CHARS)
        assertEquals(text, TelegramHtml.render(Message(Message.Text(text))))
    }

    @Test
    fun `a longer message is cut to the limit of visible text with an ellipsis`() {
        val html = TelegramHtml.render(Message(Message.Bold("head "), Message.Code("<".repeat(TELEGRAM_MAX_CHARS))))
        val shown = visible(html)
        assertEquals(TELEGRAM_MAX_CHARS, shown.length)
        assertTrue(shown.endsWith("…"), shown.takeLast(10))
        assertTrue(html.startsWith("<b>head </b><code>&lt;"), html.take(40))
        assertTrue(html.endsWith("…</code>"), html.takeLast(20))
    }

    @Test
    fun `parts after the cut are dropped`() {
        val html =
            TelegramHtml.render(
                Message(Message.Text("a".repeat(TELEGRAM_MAX_CHARS)), Message.Bold("tail")),
            )
        assertEquals("a".repeat(TELEGRAM_MAX_CHARS - 1) + "…", html)
    }

    @Test
    fun `a cut never splits a surrogate pair`() {
        val emoji = "💾"
        val text = emoji.repeat(TELEGRAM_MAX_CHARS)
        val shown = visible(TelegramHtml.render(Message(Message.Text(text))))
        assertTrue(shown.length <= TELEGRAM_MAX_CHARS, "${shown.length}")
        assertTrue(shown.dropLast(1).last().isLowSurrogate(), "ends inside a pair")
        assertEquals("…", shown.takeLast(1))
    }

    @Test
    fun `an empty part adds nothing`() {
        assertEquals("x", TelegramHtml.render(Message(Message.Bold(""), Message.Text("x"), Message.Code(""))))
    }
}
