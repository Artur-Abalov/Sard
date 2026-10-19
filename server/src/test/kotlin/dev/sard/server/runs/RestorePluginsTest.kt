// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val JSON = JsonMapper.builder().build()
private val AT: Instant = Instant.parse("2026-10-11T03:00:00Z")
private const val BACKUP =
    """{"host":"DB1","port":5432,"database":"app","user":"backup","password_ref":"pg-app","tls_mode":"disable","exclude_tables":["x"]}"""

private fun json(text: String): JsonNode = JSON.readTree(text)

/** ВК32a: what the console knows of the restore of each plugin, one entry per plugin (closes OQ-220). */
@MutFlowTest
class RestorePluginsTest {
    private val files = assertNotNull(RestorePlugins.of("files"))
    private val postgresql = assertNotNull(RestorePlugins.of("postgresql"))

    @Test
    fun `only files and postgresql are restored from the console`() {
        assertNull(RestorePlugins.of("mysql"))
        assertNull(RestorePlugins.of(""))
        assertTrue(RestorePlugins.restores("files"))
        assertTrue(RestorePlugins.restores("postgresql"))
        assertTrue(!RestorePlugins.restores("mysql"))
    }

    // --- files

    @Test
    fun `a files restore goes to the directory of the host plus the destination`() {
        val config = json("""{"destination":"etc%20data/20261010T030000Z","paths":["/etc","/srv"]}""")
        val dir = "/var/lib/sard-agent/restore"
        assertEquals("$dir/etc%20data/20261010T030000Z", MutFlow.underTest { files.target(dir, config) })
        assertEquals("/srv/r/x", files.target("/srv/r/", json("""{"destination":"x"}""")))
        assertEquals(listOf("/etc", "/srv"), MutFlow.underTest { files.paths(config) })
        assertEquals(emptyList(), files.paths(json("""{"destination":"x"}""")))
        assertEquals(false, MutFlow.underTest { files.restoreGlobals(config) })
    }

    @Test
    fun `a files restore warns that owners are not restored, whoever restores`() {
        val config = json("""{"destination":"x"}""")
        val owners = listOf(RestoreWarning.OWNERS_NOT_RESTORED)
        assertEquals(owners, MutFlow.underTest { files.warnings(config, json("{}"), true) })
        assertEquals(owners, files.warnings(config, json("{}"), false))
    }

    @Test
    fun `a files restore starts from the source name and the time of the snapshot`() {
        val defaults = MutFlow.underTest { files.defaults("etc data", AT, json("{}")) }
        assertEquals(mapOf("destination" to "etc%20data/20261011T030000Z"), defaults)
    }

    // --- postgresql: the config the form starts from

    private fun defaults(
        backup: String,
        at: Instant = AT,
    ) = MutFlow.underTest { postgresql.defaults("Q", at, json(backup)) }

    @Test
    fun `a database restore starts from the connection of the backup and a new name`() {
        assertEquals(
            mapOf(
                "host" to "DB1",
                "port" to 5432,
                "user" to "backup",
                "password_ref" to "pg-app",
                "tls_mode" to "disable",
                "database" to "app_restored_20261011",
                "restore_globals" to false,
            ),
            defaults(BACKUP),
        )
    }

    @Test
    fun `fields the backup did not set are not invented`() {
        val backup =
            """{"host":"/var/run/postgresql","database":"app","user":"u","password_ref":"p","tls_root_cert":"/ca.pem"}"""
        val got = defaults(backup)
        val expected = setOf("host", "user", "password_ref", "tls_root_cert", "database", "restore_globals")
        assertEquals(expected, got.keys)
        assertEquals("/ca.pem", got["tls_root_cert"])
    }

    @Test
    fun `the date of the new name is the UTC date of the snapshot`() {
        assertEquals("app_restored_20260102", defaults(BACKUP, Instant.parse("2026-01-02T23:59:59Z"))["database"])
        assertEquals("app_restored_20261231", defaults(BACKUP, Instant.parse("2026-12-31T00:00:00Z"))["database"])
    }

    @Test
    fun `a long name is cut at a character boundary so the new name is 63 bytes at most`() {
        val name = defaults("""{"database":"${"ж".repeat(60)}","host":"h","user":"u","password_ref":"p"}""")["database"]
        assertTrue((name as String).toByteArray().size <= 63, name)
        assertEquals("ж".repeat(22) + "_restored_20261011", name) // 44 bytes + 18 = 62; a 23rd letter would make 64
    }

    @Test
    fun `a name that fits keeps all its bytes, to the last one`() {
        val expected = "${"a".repeat(45)}_restored_20261011"
        assertEquals(expected, defaults("""{"database":"${"a".repeat(45)}"}""")["database"])
        assertEquals(expected, defaults("""{"database":"${"a".repeat(46)}"}""")["database"])
    }

    @Test
    fun `a backup config without a database falls back to the source name`() {
        assertEquals("Q_restored_20261011", defaults("{}")["database"])
    }

    // --- postgresql: target, parts, warnings

    private fun warnings(
        config: String,
        backup: String = BACKUP,
        fromBackupAgent: Boolean = true,
    ) = MutFlow.underTest { postgresql.warnings(json(config), json(backup), fromBackupAgent) }

    @Test
    fun `a database restore targets the database of the config and restores no paths`() {
        val config = json("""{"database":"app_restored_20261011","restore_globals":true}""")
        assertEquals("app_restored_20261011", MutFlow.underTest { postgresql.target("/ignored", config) })
        assertEquals(emptyList(), MutFlow.underTest { postgresql.paths(config) })
        assertEquals(true, MutFlow.underTest { postgresql.restoreGlobals(config) })
        assertEquals(false, postgresql.restoreGlobals(json("""{"database":"x"}""")))
        assertEquals(false, postgresql.restoreGlobals(json("""{"database":"x","restore_globals":false}""")))
    }

    @Test
    fun `without global objects the owners are not restored, with them the cluster changes`() {
        assertEquals(listOf(RestoreWarning.PG_OWNERS_NOT_RESTORED), warnings("""{"host":"other-db","database":"x"}"""))
        val globals = """{"host":"other-db","database":"x","restore_globals":true}"""
        assertEquals(listOf(RestoreWarning.GLOBALS_CHANGE_CLUSTER), warnings(globals))
    }

    private fun same(
        config: String,
        backup: String = BACKUP,
        fromBackupAgent: Boolean = true,
    ) = RestoreWarning.PG_SAME_CLUSTER in warnings(config, backup, fromBackupAgent)

    @Test
    fun `the same cluster is told by the agent of the backup and the same host and port`() {
        assertTrue(same("""{"host":"db1","port":5432}"""))
        assertTrue(same("""{"host":"DB1"}"""), "the port is 5432 by default")
        assertTrue(same("""{"host":"Db1","port":5432}"""), "the host is compared without case")
        assertTrue(!same("""{"host":"db1","port":5433}"""))
        assertTrue(!same("""{"host":"db9","port":5432}"""))
        assertTrue(!same("""{"host":"db1","port":5432}""", fromBackupAgent = false))
        assertTrue(!same("""{"port":5432}"""), "no host is no match")
    }

    @Test
    fun `the port of the backup is 5432 by default as well`() {
        val backup = """{"host":"db1","database":"app"}"""
        assertTrue(same("""{"host":"db1"}""", backup))
        assertTrue(same("""{"host":"db1","port":5432}""", backup))
        assertTrue(!same("""{"host":"db1","port":6432}""", backup))
    }

    @Test
    fun `the same cluster comes after the warning about owners or globals`() {
        assertEquals(
            listOf(RestoreWarning.PG_OWNERS_NOT_RESTORED, RestoreWarning.PG_SAME_CLUSTER),
            warnings("""{"host":"db1","port":5432}"""),
        )
        assertEquals(
            listOf(RestoreWarning.GLOBALS_CHANGE_CLUSTER, RestoreWarning.PG_SAME_CLUSTER),
            warnings("""{"host":"db1","restore_globals":true}"""),
        )
    }
}
