// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The scenarios tagged `@doc` of docs/specs/server/onboarding-setup.feature: the test reads files of the
 * repository and looks for the strings the scenario lists. They keep the installation (compose, scripts,
 * Makefile) and the operator's documentation in step with the first start by code.
 */
class FirstStartDocsTest {
    private val root = File("..").canonicalFile

    private fun text(path: String): String {
        val file = File(root, path)
        check(file.isFile) { "expected to run with the server module as the working directory: $file" }
        return file.readText()
    }

    private fun quickstart(): String {
        val readme = text("README.md")
        val begin = readme.indexOf("quickstart:begin")
        val end = readme.indexOf("quickstart:end")
        check(begin in 0 until end) { "README.md has no quickstart block" }
        return readme.substring(begin, end)
    }

    @Test
    fun `Compose and the environment example do not mention the administrator password`() {
        for (path in listOf("deploy/docker-compose.yml", "deploy/.env.example")) {
            assertFalse(text(path).contains("SARD_ADMIN_PASSWORD"), path)
        }
    }

    @Test
    fun `The script that generated the administrator password is gone`() {
        assertFalse(File(root, "scripts/ensure-admin-password.sh").exists())
        assertFalse(text("Makefile").contains("ensure-admin-password"))
        assertFalse(text("Makefile").contains("ADMIN_PASSWORD"))
    }

    @Test
    fun `The password variable is mentioned only in history and in the test that it is not read`() {
        val skipped = setOf(".git", "build", "node_modules", ".gradle", "dist", ".bin", ".kotlin", ".idea")
        val history = listOf("docs/sessions", "docs/adr", "docs/specs", "docs/qa").map { File(root, it) }
        val found =
            root
                .walkTopDown()
                .onEnter { dir -> dir.name !in skipped && dir !in history }
                .filter { it.isFile && it.length() < 2_000_000 }
                .filter { it != File(root, "docs/open-questions.md") }
                .filter { runCatching { it.readText().contains("SARD_ADMIN_PASSWORD") }.getOrDefault(false) }
                .map { it.relativeTo(root).path }
                .toSortedSet()
        assertEquals(
            setOf(
                "server/src/test/kotlin/dev/sard/server/FirstStartDocsTest.kt",
                "server/src/test/kotlin/dev/sard/server/onboarding/FirstStartRestartIntegrationTest.kt",
            ),
            found,
        )
    }

    @Test
    fun `The README quickstart goes through the setup code`() {
        assertFalse(quickstart().contains("SARD_ADMIN_PASSWORD"))
        val readme = text("README.md")
        assertTrue(readme.contains("docker compose logs server"))
        assertTrue(readme.contains("SARD SETUP CODE"))
        assertTrue(readme.contains("/setup"))
    }

    @Test
    fun `The operator documentation describes the first start, the password change and the recovery`() {
        val install = text("docs/operator/02-install.md")
        assertTrue(install.contains("docker compose logs server"), "02-install: logs")
        assertTrue(install.contains("SARD SETUP CODE"), "02-install: code line")
        assertFalse(text("docs/operator/03-configuration.md").contains("SARD_ADMIN_PASSWORD"))

        val security = text("docs/operator/10-security.md")
        assertTrue(security.contains("Argon2id"), "10-security: hash")
        assertTrue(security.contains("Восстановление доступа"), "10-security: recovery section")
        assertTrue(security.contains("docker compose run --rm server admin-reset"))
        assertTrue(security.contains("docker compose restart server"))

        val upgrade = text("docs/operator/07-upgrade.md")
        assertTrue(upgrade.contains("0.0.1-rc1") && upgrade.contains("docker compose down -v"), "07-upgrade: rc1")
        assertTrue(upgrade.contains("0.1.0-beta"), "07-upgrade: beta")

        val trouble = text("docs/operator/09-troubleshooting.md")
        assertTrue(
            trouble.contains("10-security.md#восстановление-доступа"),
            "09-troubleshooting: link to the section Восстановление доступа",
        )
        assertTrue(trouble.contains("setup_required") && trouble.contains("setup_completed"))
    }

    @Test
    fun `The troubleshooting section names the CA refusals and what to fix`() {
        val trouble = text("docs/operator/09-troubleshooting.md")
        for (code in listOf("CA_ORIGIN_NOT_RECORDED", "CA_MISSING")) {
            val section = trouble.substringAfter(code)
            assertTrue(section.contains("docker compose down -v"), code)
        }
        assertTrue(trouble.substringAfter("CA_MISSING").contains("SARD_PKI_IMPORT_DIR"))
    }

    @Test
    fun `The demo goes through the first start by code`() {
        val demo = text("docs/demo.md")
        assertFalse(demo.contains("SARD_ADMIN_PASSWORD"))
        assertTrue(demo.contains("docker compose logs server"))
        assertTrue(demo.contains("SARD SETUP CODE"))
    }

    @Test
    fun `The ADR of the feature is written and ADR 0021 and 0052 point to it`() {
        val adr = text("docs/adr/00XX-draft-f4a-first-start.md")
        for (section in listOf("## Контекст", "## Решение", "## Отвергнуто", "## Последствия")) {
            assertTrue(adr.contains(section), section)
        }
        assertTrue(adr.contains("CA_ORIGIN_NOT_RECORDED") && adr.contains("CA_MISSING"))
        assertTrue(text("docs/adr/0021-admin-password-login.md").contains("00XX-draft-f4a-first-start.md"))
        assertTrue(text("docs/adr/0052-ca-import.md").contains("00XX-draft-f4a-first-start.md"))
    }
}
