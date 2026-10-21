// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

/** Where the compose installation keeps the config of the built-in agent (the same path in every command). */
internal const val SELF_AGENT_CONFIG = "/etc/sard/self/agent.yaml"

private val PLAIN_WORD = Regex("[A-Za-z0-9._-]+")

/** [value] as one shell word: bare when it needs no quoting, else in single quotes. */
internal fun shellWord(value: String): String {
    if (PLAIN_WORD.matches(value)) return value
    return "'" + value.replace("'", "'\\''") + "'"
}

/**
 * The command that prints the password of [repository] on the server's machine (F4b, Р27). The server only builds
 * the text: the password is read by the agent from its own file and never reaches the server.
 */
fun passwordRevealCommand(repository: String): String =
    "docker compose exec self-agent sard-agent repo password ${shellWord(repository)} " +
        "--reveal --config $SELF_AGENT_CONFIG"

/**
 * The command that issues a recovery key for [repository] on the server's machine (V3, КС2): a one-off container of
 * the built-in agent's image, as the one that adds a repository. `--if-missing` makes a repeat, or a repository whose
 * key `repo add` issued already, answer "unchanged". The key is printed in the terminal once and never reaches the
 * server, which learns the flag from the next Register.
 */
fun recoveryKeyCommand(repository: String): String =
    "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER " +
        "--cap-add SETUID --cap-add SETGID self-agent repo key add ${shellWord(repository)} --for recovery " +
        "--if-missing --config $SELF_AGENT_CONFIG"
