// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.shell

private val PLAIN_WORD = Regex("[A-Za-z0-9._-]+")

/**
 * [value] as one shell word: bare when it needs no quoting, else in single quotes, with a quote inside it escaped.
 * Always answers: for a command of the built-in agent, whose repository name the server validates itself.
 *
 * Differs from [shellWordOrNull] on purpose: the commands for other hosts refuse what cannot be quoted safely.
 */
fun shellWord(value: String): String {
    if (PLAIN_WORD.matches(value)) return value
    return "'" + value.replace("'", "'\\''") + "'"
}

/**
 * [value] as one shell word like [shellWord], but null for a value with a quote or a control character: the spec
 * (restic-keys.feature, КС6, КС7) gives no command for such a name, rather than a command that is hard to read.
 */
fun shellWordOrNull(value: String): String? =
    when {
        PLAIN_WORD.matches(value) -> value
        value.any { it == '\'' || it.isISOControl() } -> null
        else -> "'$value'"
    }
