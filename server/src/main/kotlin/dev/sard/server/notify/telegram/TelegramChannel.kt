// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import dev.sard.server.notify.Message
import dev.sard.server.notify.NotificationChannel
import dev.sard.server.notify.SendOutcome

/** Telegram: one chat for every tenant at stage 1 (OQ-017, answer В10). */
class TelegramChannel(
    private val api: TelegramBotApi,
    private val chatId: String,
) : NotificationChannel {
    override val name = NAME

    override fun send(message: Message): SendOutcome = api.sendMessage(chatId, TelegramHtml.render(message))

    companion object {
        const val NAME = "telegram"
    }
}
