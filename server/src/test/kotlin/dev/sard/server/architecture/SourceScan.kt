// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import java.io.File

private const val ROOT = "dev.sard.server"
internal val MAIN = File("src/main/kotlin/dev/sard/server")
internal val PACKAGE = Regex("""^package\s+${Regex.escape(ROOT)}\.(\w+)""")
internal val IMPORT = Regex("""^import\s+${Regex.escape(ROOT)}\.(\w+)\.""")

/** The Kotlin sources under [dir]; fails when the tests do not run with the server module as the working directory. */
internal fun kotlinFiles(dir: File = MAIN): List<File> {
    check(MAIN.isDirectory) { "expected to run with the server module as the working directory: $MAIN" }
    return dir.walkTopDown().filter { it.isFile && it.extension == "kt" }.toList()
}
