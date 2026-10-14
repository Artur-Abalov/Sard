// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val PAGE = "docs/operator/12-server-recovery.md"
private const val PAGE_EN = "docs/en/operator/12-server-recovery.md"
private val DRILL_MARK = Regex("""<!-- drill: (\S+) -->\s*```bash\n(.*?)\n```""", RegexOption.DOT_MATCHES_ALL)
private val BASH_BLOCK = Regex("""```bash\n(.*?)\n```""", RegexOption.DOT_MATCHES_ALL)
private val SUBSTITUTION = Regex("""<([\p{L}][\p{L}\p{N}_.-]*)>""")
private val SECRET_WORD = Regex("(?i)парол|секрет|ключ|password|secret|key|token")

/**
 * The scenarios tagged `@doc` of docs/specs/server/server-recovery.feature: the test reads files of the repository
 * and looks for what the scenario lists, so the page "Восстановление сервера" and the pages around it stay in step
 * with the procedure the drill (test/e2e ServerRecoveryDrillTest) runs.
 */
class ServerRecoveryDocsTest {
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

    /** The part of the page under the heading that starts with [heading], up to the next heading of its level. */
    private fun section(
        path: String,
        heading: String,
    ): String {
        val page = text(path)
        val start = page.indexOf("\n## $heading")
        assertTrue(start >= 0, "$path has no section \"$heading\"")
        val next = page.indexOf("\n## ", start + 1)
        return page.substring(start, if (next < 0) page.length else next)
    }

    private fun bashLines(path: String) = BASH_BLOCK.findAll(text(path)).flatMap { it.groupValues[1].lines() }.toList()

    private fun drillBlocks(path: String): Map<String, String> {
        val marked = DRILL_MARK.findAll(text(path))
        return marked.associate { it.groupValues[1] to it.groupValues[2] }
    }

    @Test
    fun `Страница восстановления есть в оглавлении раздела оператора`() {
        val row = text("docs/operator/README.md").lines().singleOrNull { it.startsWith("| 12 |") }
        assertTrue(row != null && "(12-server-recovery.md)" in row, "no row 12 with a link: $row")
    }

    @Test
    fun `Раздел самобэкапа ведёт на страницу восстановления`() {
        val section = section("docs/operator/06-data-and-backup.md", "Самобэкап")
        val heading = section.substringAfter("### Восстановление из самобэкапа (F7)", "")
        assertTrue(heading.isNotEmpty(), "the heading \"Восстановление из самобэкапа (F7)\" is gone")
        val body = heading.substringBefore("\n## ").substringBefore("\n### ")
        assertTrue("12-server-recovery.md" in body, body)
    }

    @Test
    fun `Страница называет три вещи, нужные для восстановления`() {
        assertMentions(PAGE, "пароль репозитория", "карточка восстановления", "recovery-card")
    }

    @Test
    fun `Раздел что нужно говорит, что вход после восстановления прежним паролем администратора`() {
        val needs = section(PAGE, "Что нужно")
        for (needle in listOf("пароль администратора", "тот же", "вместе с карточкой", "admin-reset")) {
            assertTrue(needle in needs, "\"Что нужно\" does not mention \"$needle\"")
        }
    }

    @Test
    fun `Страница выбирает снимки по тегу и путям и сверяет их с карточкой`() {
        assertMentions(
            PAGE,
            "postgresql.part=database",
            "--path /var/lib/sard/pki",
            "restic cat config",
            "repositoryId",
            "caFingerprint",
        )
    }

    @Test
    fun `Каждый шаг страницы называет способ проверки и что смотреть при неудаче`() {
        for (step in 1..6) {
            val section = section(PAGE, "Шаг $step.")
            assertTrue("```bash" in section, "step $step has no block of commands")
            assertTrue(Regex("(?m)^\\*\\*Проверка\\.\\*\\*").containsMatchIn(section), "step $step has no \"Проверка\"")
            val failed = Regex("(?m)^\\*\\*Если не вышло\\.\\*\\*")
            assertTrue(failed.containsMatchIn(section), "step $step has no \"Если не вышло\"")
        }
    }

    @Test
    fun `Страница показывает restic из образа агента и restic, поставленный отдельно`() {
        assertMentions(PAGE, "/usr/libexec/sard/restic", "sard-agent:", "restic -r")
    }

    @Test
    fun `Страница поднимает сначала только PostgreSQL и восстанавливает дамп без владельцев`() {
        assertMentions(PAGE, "docker compose up -d --wait postgres", "pg_restore", "--no-owner")
    }

    @Test
    fun `Страница сверяет отпечаток CA по строке лога сервера`() {
        assertMentions(PAGE, "CA fingerprint=", "origin=existing")
    }

    @Test
    fun `Страница заменяет прежний встроенный агент и перепривязывает самобэкап`() {
        assertMentions(
            PAGE,
            "confirm=sard-self",
            "repo add",
            "--password-from-file",
            "/api/v1/self-backup/repository",
            "location",
            "keys-confirmation",
        )
    }

    @Test
    fun `Страница объясняет агентов с CERT_UNKNOWN и где это видно`() {
        assertMentions(
            PAGE,
            "CERT_UNKNOWN",
            "journalctl -u sard-agent",
            "enroll --force",
            "systemctl restart sard-agent",
        )
    }

    @Test
    fun `Страница предупреждает об агентах, отозванных после снимка`() {
        val paragraphs = text(PAGE).split(Regex("\n\\s*\n"))
        assertTrue(
            paragraphs.any { "отозван" in it && "после снимка" in it && "revoke" in it },
            "no paragraph about agents revoked after the snapshot",
        )
    }

    @Test
    fun `Страница объясняет смену имени сервера`() {
        assertMentions(PAGE, "server.address", "SARD_PKI_SERVER_NAMES", "08-migrate-and-remove.md")
    }

    @Test
    fun `Страница называет потерянные и догоняющие запуски после восстановления`() {
        assertMentions(PAGE, "lost", "catch_up", "catchUp")
    }

    @Test
    fun `Страница и раздел неполадок называют отказ на базе от более новой версии`() {
        assertMentions(PAGE, "DATABASE_NEWER_THAN_SERVER")
        assertMentions("docs/operator/09-troubleshooting.md", "DATABASE_NEWER_THAN_SERVER", "SARD_VERSION")
        val row = text("docs/operator/09-troubleshooting.md").lines().single { "DATABASE_NEWER_THAN_SERVER" in it }
        assertTrue("SARD_VERSION" in row && ".env" in row, "the row does not say to raise SARD_VERSION in .env: $row")
    }

    @Test
    fun `Страница называет отказы старта CA при несовпадающей паре`() {
        assertMentions(PAGE, "CA_ORIGIN_NOT_RECORDED", "CA_MISSING")
    }

    @Test
    fun `Команды страницы не содержат секретов и подстановок для них`() {
        val lines = bashLines(PAGE)
        assertTrue(lines.isNotEmpty())
        val forbidden =
            listOf(
                Regex("RESTIC_PASSWORD="),
                Regex("AWS_SECRET_ACCESS_KEY="),
                Regex("--password\\s+\\S"),
                Regex("--secret-key\\s+\\S"),
            )
        for (line in lines) {
            for (pattern in forbidden) assertTrue(!pattern.containsMatchIn(line), "secret in a command: $line")
            for (name in SUBSTITUTION.findAll(line).map { it.groupValues[1] }) {
                assertTrue(!SECRET_WORD.containsMatchIn(name), "a substitution for a secret: <$name> in $line")
            }
        }
    }

    @Test
    fun `Секреты вводятся без эха в файлы с правами 600`() {
        assertMentions(PAGE, "read -rs", "chmod 600", "chmod 700")
    }

    @Test
    fun `Каждый docker compose up страницы имеет офлайн-вариант`() {
        assertMentions(PAGE, "--pull never", "11-offline.md")
        val ups = bashLines(PAGE).filter { "docker compose up" in it }
        assertTrue(ups.isNotEmpty())
        assertEquals(emptyList(), ups.filter { "--pull never" !in it })
    }

    @Test
    fun `Раздел офлайн-установки перечисляет образ агента в архиве`() {
        val table = text("docs/operator/11-offline.md").lines().filter { it.startsWith("|") }
        assertTrue(table.any { "sard-agent" in it }, table.toString())
    }

    @Test
    fun `Страница содержит измеренное на учениях время`() {
        val section = section(PAGE, "Сколько это займёт")
        val header = section.lines().first { it.startsWith("|") }
        val columns = listOf("Дата", "Версия", "Размер дампа", "Размер базы")
        val minutes = listOf("до сервера", "до агентов", "до самобэкапа")
        for (column in columns + minutes) {
            assertTrue(column in header, "no column \"$column\" in $header")
        }
        val rows = section.lines().filter { it.startsWith("|") }.drop(2)
        val digit = Regex("""\d""")
        val withNumbers = rows.filter { row -> row.split('|').count { digit.containsMatchIn(it) } >= 6 }
        assertTrue(withNumbers.isNotEmpty(), "no row with numbers: $rows")
    }

    @Test
    fun `Английская страница восстановления содержит те же блоки учений`() {
        val russian = drillBlocks(PAGE)
        val english = drillBlocks(PAGE_EN)
        assertTrue(russian.isNotEmpty(), "the page has no block marked for the drill")
        assertEquals(russian.keys, english.keys)
        for ((id, block) in russian) assertEquals(block, english.getValue(id), "block $id differs")
    }
}
