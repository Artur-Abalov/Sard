// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

/** Whether Enroll may register an agent that a token other than the built-in one names (F4b, K17). */
fun interface RegistrationGate {
    fun open(): Boolean
}
