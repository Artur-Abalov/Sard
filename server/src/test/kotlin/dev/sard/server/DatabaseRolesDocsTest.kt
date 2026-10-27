// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertTrue

/**
 * Rule "Документация оператора описывает роли, пароли и аварийный доступ" (DB2): section 16 of the operator's guide.
 */
class DatabaseRolesDocsTest {
    private val guide: String
        get() {
            val file = File("../docs/operator/16-postgresql.md")
            check(file.isFile) { "expected to run with the server module as the working directory: $file" }
            return file.readText()
        }

    /** The text of the subsection [title] (a `## ` heading) up to the next one; fails when there is none. */
    private fun subsection(title: String): String {
        val start = guide.indexOf("\n## $title\n")
        assertTrue(start >= 0, "no subsection '$title' in section 16")
        val body = guide.substring(start + 1)
        return body.substringBefore("\n## ", body).let { it.substringAfter("\n") }
    }

    private fun assertMentions(
        title: String,
        vararg parts: String,
    ) {
        val text = subsection(title)
        for (part in parts) assertTrue(part in text, "'$title' does not mention '$part'")
    }

    @Test
    fun `Подраздел Роли называет все пять ролей Sard`() {
        assertMentions("Роли", "sard_owner", "sard_migrator", "sard_app", "sard_backup", "sard_monitor")
    }

    @Test
    fun `Подраздел Где пароли называет тома, видимость и самобэкап`() {
        assertMentions(
            "Где пароли",
            "sard_sard-db-migrate",
            "sard_sard-db-app",
            "sard_sard-db-backup",
            "не входят в самобэкап",
            "случаен и не хранится нигде",
        )
    }

    @Test
    fun `Подраздел Смена пароля роли даёт команды процедуры`() {
        assertMentions(
            "Смена пароля роли",
            "db-init /run/sard-db/app/password",
            "docker compose up -d",
            "docker compose restart server",
        )
    }

    @Test
    fun `Подраздел Аварийный доступ называет локальный сокет и закрытый вход по сети`() {
        assertMentions(
            "Аварийный доступ",
            "docker compose exec postgres psql -U sard",
            "по локальному сокету",
            "по сети",
        )
    }

    @Test
    fun `Подраздел Внешний кластер называет bootstrap, пароли, migrate и переменные сервера`() {
        assertMentions(
            "Внешний кластер",
            "bootstrap.sql",
            "\\password sard_app",
            "migrate",
            "SARD_DB_USER=sard_app",
            "SARD_DB_PASSWORD_FILE",
        )
    }

    @Test
    fun `Документация называет переход на enterprise необратимым`() {
        val text = subsection("Переход на enterprise")

        assertTrue("применение" in text && "дополнительных миграций" in text, text)
        assertTrue("обратного пути нет" in text, text)
    }
}
