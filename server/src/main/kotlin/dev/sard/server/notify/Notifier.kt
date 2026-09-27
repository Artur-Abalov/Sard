// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

/** Sends reports and alerts: Telegram, email, webhook (roadmap: notifications, stage 2). */
interface Notifier {
    fun send(
        subject: String,
        body: String,
    )
}
