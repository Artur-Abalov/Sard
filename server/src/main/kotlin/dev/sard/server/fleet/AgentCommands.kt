// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.shell.shellWordOrNull
import java.util.UUID

private const val ADDRESS_PLACEHOLDER = "<address>"
private const val FILE_PLACEHOLDER = "<file>"
private const val LABEL_HEX_DIGITS = 8

/** The command that issues a recovery key on a source host; null when [name] cannot be quoted safely (КС6). */
internal fun recoveryKeyAddCommand(name: String): String? {
    val word = shellWordOrNull(name) ?: return null
    return "sudo sard-agent repo key add $word --for recovery"
}

/** The command that issues a key for [verifierId] on a source host; null when [name] cannot be quoted safely (КС7). */
internal fun verifierKeyAddCommand(
    name: String,
    verifierId: UUID,
): String? {
    val label = "vrf-" + verifierId.toString().replace("-", "").take(LABEL_HEX_DIGITS)
    return shellWordOrNull(name)?.let { "sudo sard-agent repo key add $it --for verifier --label $label" }
}

/** The command that issues a key for a verifier that is not installed yet; null when [name] cannot be quoted. */
internal fun firstVerifierKeyAddCommand(name: String): String? =
    shellWordOrNull(name)?.let { "sudo sard-agent repo key add $it --for verifier" }

/** The command that connects a repository on the first verifier: its name, address and key file are its own. */
internal const val FIRST_VERIFIER_REPO_ADD =
    "sudo sard-agent repo add <name> $ADDRESS_PLACEHOLDER --password-from-file $FILE_PLACEHOLDER"

/** The template of the command that connects the repository on a verifier; null when [name] cannot be quoted (КС7). */
internal fun verifierRepoAddTemplate(name: String): String? =
    shellWordOrNull(name)?.let {
        "sudo sard-agent repo add $it $ADDRESS_PLACEHOLDER --password-from-file $FILE_PLACEHOLDER"
    }
