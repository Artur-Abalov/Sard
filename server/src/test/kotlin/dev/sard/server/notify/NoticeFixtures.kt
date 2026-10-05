// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.notify.telegram.FakeBotApi
import tools.jackson.databind.json.JsonMapper

private val BODY = JsonMapper.builder().build()

/** Stands in for a channel: delivers everything and keeps what it was given. */
class CapturingChannel : NotificationChannel {
    val sent = mutableListOf<Message>()
    override val name = "telegram"

    override fun send(message: Message): SendOutcome {
        sent += message
        return SendOutcome.Delivered
    }
}

/** The text of a message without markup: the parts joined. */
fun Message.plainText(): String = parts.joinToString("") { it.text }

/** The HTML text of the [index]th request the fake bot received. */
fun FakeBotApi.sentHtml(index: Int = 0): String = BODY.readTree(requests[index].body).path("text").asString()

/** The text a person sees in Telegram: tags removed, entities decoded. */
fun FakeBotApi.shownText(index: Int = 0): String =
    sentHtml(index)
        .replace(Regex("<[^>]*>"), "")
        .replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&amp;", "&")
