// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.shell.shellWord

/** Where the compose installation keeps the config of the built-in agent (the same path in every command). */
internal const val SELF_AGENT_CONFIG = "/etc/sard/self/agent.yaml"

/**
 * The command that prints the password of [repository] on the server's machine (F4b, Р27). The server only builds
 * the text: the password is read by the agent from its own file and never reaches the server.
 */
fun passwordRevealCommand(repository: String): String =
    "docker compose exec self-agent sard-agent repo password ${shellWord(repository)} " +
        "--reveal --config $SELF_AGENT_CONFIG"

/** Restarts the sidecar so it reads what was changed and Registers again. */
internal const val RESTART = "docker compose restart self-agent"

/**
 * A one-off container of the built-in agent's image, as root with only the five capabilities that `repo add` and
 * `repo key add` need for files, running `sard-agent` with [words] (the words are already shell-safe).
 */
internal fun oneOffAgent(vararg words: String): String =
    "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER " +
        "--cap-add SETUID --cap-add SETGID self-agent " + words.joinToString(" ")

/**
 * The command that issues a recovery key for [repository] on the server's machine (V3, КС2): a one-off container of
 * the built-in agent's image, as the one that adds a repository. `--if-missing` makes a repeat, or a repository whose
 * key `repo add` issued already, answer "unchanged". The key is printed in the terminal once and never reaches the
 * server, which learns the flag from the next Register. A one-off container has no systemd, so the agent cannot
 * restart the sidecar itself: `--no-restart` and an explicit restart make it Register again.
 */
fun recoveryKeyCommand(repository: String): String =
    oneOffAgent(
        "repo",
        "key",
        "add",
        shellWord(repository),
        "--for",
        "recovery",
        "--if-missing",
        "--no-restart",
        "--config",
        SELF_AGENT_CONFIG,
    ) + " && $RESTART"
