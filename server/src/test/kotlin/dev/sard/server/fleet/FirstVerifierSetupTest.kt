// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val DB1 = UUID(0, 1)
private val WEB1 = UUID(0, 2)

/** V6: the commands to set up the first verifier, from the repositories the sources of the installation write to. */
@MutFlowTest
class FirstVerifierSetupTest {
    private fun setup(vararg pairs: SourceRepository) = MutFlow.underTest { FirstVerifierSetup.of(pairs.toList()) }

    @Test
    fun `each agent and repository of the sources gets the command for its host`() {
        val db = SourceRepository(DB1, "db1.example.com", "pg")
        val web = SourceRepository(WEB1, "web1.example.com", "www")

        val setup = setup(db, web)

        assertEquals(
            listOf(
                Triple("db1.example.com", "pg", "sudo sard-agent repo key add pg --for verifier"),
                Triple("web1.example.com", "www", "sudo sard-agent repo key add www --for verifier"),
            ),
            setup.repositories.map { Triple(it.sourceHostname, it.repositoryName, it.keyAddCommand) },
        )
    }

    @Test
    fun `the same repository of one agent is one entry, of two agents two`() {
        val setup =
            setup(
                SourceRepository(DB1, "db1", "pg"),
                SourceRepository(DB1, "db1", "pg"),
                SourceRepository(WEB1, "web1", "pg"),
            )

        assertEquals(listOf("db1", "web1"), setup.repositories.map { it.sourceHostname })
    }

    @Test
    fun `entries go by host, then by repository`() {
        val setup =
            setup(
                SourceRepository(WEB1, "web1", "a"),
                SourceRepository(DB1, "db1", "z"),
                SourceRepository(DB1, "db1", "b"),
            )

        val order = setup.repositories.map { it.sourceHostname to it.repositoryName }
        assertEquals(listOf("db1" to "b", "db1" to "z", "web1" to "a"), order)
    }

    @Test
    fun `a name that cannot be quoted safely gets no command`() {
        val setup = setup(SourceRepository(DB1, "db1", "o'brien"))

        assertNull(setup.repositories.single().keyAddCommand)
    }

    @Test
    fun `a name with a space is quoted, one with only a quote or a control character gets no command`() {
        fun command(name: String) = setup(SourceRepository(DB1, "db1", name)).repositories.single().keyAddCommand

        assertEquals("sudo sard-agent repo key add 'my repo' --for verifier", command("my repo"))
        assertNull(command("'"))
        assertNull(command("\t"))
        assertNull(command("a\nb"))
    }

    @Test
    fun `the verifier's own repo add is a template with placeholders, over a read-only account`() {
        val setup = setup()

        assertEquals("sudo sard-agent repo add <name> <address> --password-from-file <file>", setup.verifierRepoAdd)
        assertEquals(emptyList(), setup.repositories)
    }
}
