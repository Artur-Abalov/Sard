// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path

/**
 * A password the server or the migrate step is given (DB2): the file named by `<variable>_FILE`, which db-init writes
 * into a volume of its own for each consumer, or else the variable itself (an external cluster, a test). The file is
 * read whole, without a line feed at its end, so a password typed into a file by hand works too. A password is never
 * part of a message.
 */
object PasswordSource {
    fun resolve(
        env: Map<String, String>,
        variable: String,
    ): String {
        val file = env["${variable}_FILE"]?.takeIf { it.isNotBlank() }
        return if (file == null) env[variable].orEmpty() else read("${variable}_FILE", file)
    }

    private fun read(
        variable: String,
        file: String,
    ): String =
        try {
            Files.readString(Path.of(file)).trimEnd('\n', '\r')
        } catch (e: IOException) {
            val why = "$variable=$file cannot be read (${e.javaClass.simpleName})"
            throw IllegalStateException("$why; see docs/operator/16-postgresql.md", e)
        }
}
