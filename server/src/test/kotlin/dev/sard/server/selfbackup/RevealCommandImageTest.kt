// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F4b, Р27: `docker compose exec` bypasses ENTRYPOINT, so the program of the reveal command must
 * resolve through PATH in the agent image (deploy/agent/Dockerfile, both variants: one file).
 */
class RevealCommandImageTest {
    private val dockerfile =
        listOf("../deploy/agent/Dockerfile", "deploy/agent/Dockerfile")
            .map(::File)
            .first { it.isFile }
            .readLines()
            .joinToString("\n")
            .replace("\\\n", " ")

    private val standardPath = listOf("/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin")

    private fun program(): String =
        passwordRevealCommand("self")
            .removePrefix("docker compose exec self-agent ")
            .substringBefore(' ')

    private fun entrypoint(): String = Regex("""(?m)^ENTRYPOINT \["([^"]+)"""").find(dockerfile)!!.groupValues[1]

    @Test
    fun `Команда показа пароля находит программу в образе агента`() {
        val name = program()
        val links =
            Regex("""ln -s\w* (\S+) (\S+)""")
                .findAll(dockerfile)
                .associate { it.groupValues[2] to it.groupValues[1] }
        val resolved =
            standardPath
                .map { "$it/$name" }
                .firstNotNullOfOrNull { links[it] }
        assertTrue(
            name == entrypoint() || resolved == entrypoint(),
            "'$name' is neither the ENTRYPOINT nor a symlink on PATH to it; links: $links",
        )
        assertEquals("sard-agent", name)
    }
}
