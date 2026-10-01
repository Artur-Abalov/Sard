// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import dev.sard.server.notify.Message

/** Telegram's limit for a message, in UTF-16 code units of the text after entity parsing. */
internal const val TELEGRAM_MAX_CHARS = 4096

private const val ELLIPSIS = "…"

/**
 * Renders a [Message] as Telegram HTML (`parse_mode=HTML`). Every part is plain text and is
 * escaped here, so no user data (source names, host names, agent messages) becomes markup. A
 * message over [TELEGRAM_MAX_CHARS] visible characters is cut and ends with an ellipsis; tags
 * stay balanced because each part is closed on its own.
 */
object TelegramHtml {
    fun render(message: Message): String {
        val parts = fit(message.parts)
        return parts.joinToString("") { wrap(it, escape(it.text)) }
    }

    private fun fit(parts: List<Message.Part>): List<Message.Part> {
        if (parts.sumOf { it.text.length } <= TELEGRAM_MAX_CHARS) return parts
        val budget = TELEGRAM_MAX_CHARS - ELLIPSIS.length
        val kept = mutableListOf<Message.Part>()
        var used = 0
        for (part in parts) {
            if (used + part.text.length >= budget) {
                kept += retext(part, cut(part.text, budget - used) + ELLIPSIS)
                break
            }
            kept += part
            used += part.text.length
        }
        return kept
    }

    /** The first [length] code units, one fewer if that would split a surrogate pair. */
    private fun cut(
        text: String,
        length: Int,
    ): String {
        val end = if (length > 0 && text[length - 1].isHighSurrogate()) length - 1 else length
        return text.substring(0, end)
    }

    private fun retext(
        part: Message.Part,
        text: String,
    ): Message.Part =
        when (part) {
            is Message.Text -> Message.Text(text)
            is Message.Bold -> Message.Bold(text)
            is Message.Code -> Message.Code(text)
        }

    private fun wrap(
        part: Message.Part,
        html: String,
    ): String =
        when {
            html.isEmpty() -> ""
            part is Message.Bold -> "<b>$html</b>"
            part is Message.Code -> "<code>$html</code>"
            else -> html
        }

    private fun escape(text: String) = text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}
