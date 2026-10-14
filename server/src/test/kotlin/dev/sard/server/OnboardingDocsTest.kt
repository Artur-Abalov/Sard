// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertTrue

/**
 * The scenarios tagged `@doc` of docs/specs/server/onboarding-self-backup.feature: the test reads files of the
 * repository and looks for the strings the scenario lists, so the operator's documentation stays in step with the
 * steps of the self-backup and of the key copy.
 */
class OnboardingDocsTest {
    private val root = File("..").canonicalFile

    private fun text(path: String): String {
        val file = File(root, path)
        check(file.isFile) { "expected to run with the server module as the working directory: $file" }
        return file.readText()
    }

    private fun assertMentions(
        path: String,
        vararg strings: String,
    ) {
        val text = text(path)
        for (needle in strings) assertTrue(needle in text, "$path does not mention \"$needle\"")
    }

    @Test
    fun `Документация установки описывает шаги самобэкапа и копии ключей`() {
        assertMentions(
            "docs/operator/02-install.md",
            "repository-command",
            "self-agent repo add",
            "repo password",
            "keys-confirmation",
            "onboarding_incomplete",
        )
    }

    @Test
    fun `Документация обновления описывает ограничение обновлённой установки`() {
        assertMentions("docs/operator/07-upgrade.md", "new_agents", "ONBOARDING_INCOMPLETE")
    }

    @Test
    fun `Раздел неполадок называет отказы онбординга`() {
        assertMentions(
            "docs/operator/09-troubleshooting.md",
            "onboarding_incomplete",
            "first_backup_pending",
            "ONBOARDING_INCOMPLETE",
            "self_agent_missing",
        )
    }

    @Test
    fun `Раздел самобэкапа ссылается на мастер вместо ручной привязки`() {
        assertMentions("docs/operator/06-data-and-backup.md", "Восстановление из самобэкапа (F7)", "recovery-card")
    }
}
