// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.enrollment.AgentEndpoint
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val DEB = "sard-agent_v1.4.0_linux_amd64.deb"
private const val RPM = "sard-agent-v1.4.0.x86_64.rpm"
private const val TAR = "sard-agent_v1.4.0_linux_arm64.tar.gz"
private val KEY = ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d")
private val BASE = DownloadsUrl.resolve("", AgentEndpoint("sard.corp.example:9090"), 8080)
private const val ADDRESS = "sard.corp.example:9090"

private fun commands() = InstallCommands(BASE, AgentEndpoint(ADDRESS), KEY)

private fun deb(
    signed: Boolean = true,
    fetch: FetchTool = FetchTool.CURL,
) = ReleasePackage(DEB, InstallFormat.DEB, signed, fetch)

private fun rpm(
    signed: Boolean = true,
    fetch: FetchTool = FetchTool.CURL,
) = ReleasePackage(RPM, InstallFormat.RPM, signed, fetch)

private fun tar(
    signed: Boolean = true,
    fetch: FetchTool = FetchTool.CURL,
) = ReleasePackage(TAR, InstallFormat.TAR, signed, fetch)

private fun List<InstallStep>.of(kind: StepKind) = single { it.kind == kind }

private fun List<InstallStep>.lines(kind: StepKind) = of(kind).commands

private val INSTALL_KINDS =
    listOf(
        StepKind.DOWNLOAD,
        StepKind.CHECKSUM,
        StepKind.SIGNATURE,
        StepKind.INSTALL,
        StepKind.CONFIGURE,
        StepKind.ENROLL,
        StepKind.REPO_INIT,
        StepKind.START,
    )

/** The commands of the install block, rule "Шаги идут в порядке установки, каждый — отдельной командой". */
@MutFlowTest
class InstallCommandsTest {
    private fun install(pkg: ReleasePackage) = MutFlow.underTest { commands().install(pkg) }

    private fun upgrade(pkg: ReleasePackage) = MutFlow.underTest { commands().upgrade(pkg) }

    @Test
    fun `Шаги deb идут в установленном порядке`() {
        assertEquals(INSTALL_KINDS, install(deb()).map { it.kind })
    }

    @Test
    fun `Необязательна только проверка подписи`() {
        for (step in install(deb())) assertEquals(step.kind == StepKind.SIGNATURE, step.optional, step.kind.name)
    }

    @Test
    fun `Скачивание забирает пакет, SHA256SUMS и подпись с адреса раздачи`() {
        val base = "http://sard.corp.example:8080/downloads/agent"
        assertEquals(
            listOf(
                "curl -fsSLO $base/$DEB",
                "curl -fsSLO $base/SHA256SUMS",
                "curl -fsSLO $base/SHA256SUMS.minisig",
            ),
            install(deb()).lines(StepKind.DOWNLOAD),
        )
    }

    @Test
    fun `Команда скачивания падает с ненулевым кодом при ответе 404`() {
        assertTrue(install(deb()).lines(StepKind.DOWNLOAD).all { it.startsWith("curl -f") })
    }

    @Test
    fun `Выбор wget даёт команды скачивания через wget`() {
        val lines = install(deb(fetch = FetchTool.WGET)).lines(StepKind.DOWNLOAD)
        assertEquals(3, lines.size)
        assertTrue(lines.all { it.startsWith("wget ") && "curl" !in it }, lines.toString())
        assertTrue(lines.all { it.contains("http://sard.corp.example:8080/downloads/agent/") }, lines.toString())
    }

    @Test
    fun `Неподписанный релиз не даёт шага подписи и не скачивает подпись`() {
        val steps = install(deb(signed = false))
        assertEquals(INSTALL_KINDS - StepKind.SIGNATURE, steps.map { it.kind })
        assertFalse(steps.flatMap { it.commands }.any { "minisig" in it })
    }

    @Test
    fun `Проверка суммы сверяет только скачанный пакет`() {
        val line = install(deb()).lines(StepKind.CHECKSUM).single()
        assertEquals("grep '  $DEB\$' SHA256SUMS | sha256sum -c -", line)
    }

    @Test
    fun `Шаг подписи вызывает minisign с ключом строкой`() {
        assertEquals(
            listOf("minisign -Vm SHA256SUMS -P ${KEY.publicKey}"),
            install(deb()).lines(StepKind.SIGNATURE),
        )
    }

    @Test
    fun `Установка deb ставит скачанный файл от root`() {
        assertEquals(listOf("sudo dpkg -i $DEB"), install(deb()).lines(StepKind.INSTALL))
    }

    @Test
    fun `Установка из архива даёт ту же раскладку, что deb`() {
        val dir = "sard-agent_v1.4.0_linux_arm64"
        assertEquals(
            listOf(
                "tar -xzf $TAR",
                "sudo sh -c 'grep -q \"^sard-agent:\" /etc/passwd || useradd --system --no-create-home " +
                    "--home-dir /var/lib/sard-agent --shell /usr/sbin/nologin --user-group sard-agent'",
                "sudo install -d -m 0755 /usr/libexec/sard",
                "sudo install -m 0755 $dir/sard-agent $dir/restic /usr/libexec/sard/",
                "sudo cp -sf /usr/libexec/sard/sard-agent /usr/bin/sard-agent",
                "sudo install -m 0644 $dir/sard-agent.service /usr/lib/systemd/system/sard-agent.service",
                "sudo install -d -o root -g sard-agent -m 0750 /etc/sard",
                "sudo install -d -o sard-agent -g sard-agent -m 0700 " +
                    "/etc/sard/tls /etc/sard/secrets /var/cache/sard/restic",
                "sudo install -m 0644 $dir/agent.example.yaml /etc/sard/agent.example.yaml",
                "sudo systemctl daemon-reload",
            ),
            install(tar()).lines(StepKind.INSTALL),
        )
    }

    @Test
    fun `Шаг конфигурации не перезаписывает существующий agent_yaml`() {
        assertEquals(
            listOf(
                "sudo sh -c '[ -e /etc/sard/agent.yaml ] || " +
                    "{ cp /etc/sard/agent.example.yaml /etc/sard/agent.yaml && " +
                    "sed -i \"s|address: sard.example.com:9090|address: sard.corp.example:9090|\" " +
                    "/etc/sard/agent.yaml; }'",
            ),
            install(deb()).lines(StepKind.CONFIGURE),
        )
    }

    @Test
    fun `Регистрация выполняется от имени пользователя службы с заполнителем токена`() {
        assertEquals(
            listOf("sudo -u sard-agent sard-agent enroll --server sard.corp.example:9090 --token <TOKEN>"),
            install(deb()).lines(StepKind.ENROLL),
        )
    }

    @Test
    fun `Создание репозитория выполняется от имени пользователя службы`() {
        assertEquals(
            listOf("sudo -u sard-agent sard-agent repo init --generate-password main"),
            install(deb()).lines(StepKind.REPO_INIT),
        )
    }

    @Test
    fun `Последний шаг включает и запускает службу`() {
        assertEquals(listOf("sudo systemctl enable --now sard-agent.service"), install(deb()).lines(StepKind.START))
    }

    @Test
    fun `Команды используют только базовые утилиты`() {
        val allowed =
            setOf(
                "sh",
                "sha256sum",
                "grep",
                "curl",
                "wget",
                "tar",
                "install",
                "useradd",
                "cp",
                "sed",
                "dpkg",
                "rpm",
                "systemctl",
                "sudo",
                "sard-agent",
            )
        val wget = FetchTool.WGET
        val packages =
            listOf(deb(), deb(fetch = wget), rpm(), rpm(fetch = wget), tar(), tar(fetch = wget), deb(signed = false))
        for (pkg in packages) {
            for (step in install(pkg) + upgrade(pkg)) {
                if (step.kind == StepKind.SIGNATURE) continue
                val used = step.commands.flatMap(::utilities).toSet()
                assertTrue(allowed.containsAll(used), "${step.kind}: ${used - allowed}")
            }
        }
    }

    @Test
    fun `Команда enroll содержит заполнитель вместо токена и в ответе нет префикса sard_`() {
        val all = install(deb()) + install(tar()) + upgrade(deb())
        assertFalse(all.flatMap { it.commands }.any { "sard_" in it })
    }

    @Test
    fun `Обновление deb ставит пакет поверх без удаления`() {
        val steps = upgrade(deb())
        val kinds = listOf(StepKind.DOWNLOAD, StepKind.CHECKSUM, StepKind.SIGNATURE, StepKind.UPGRADE)
        assertEquals(kinds, steps.map { it.kind })
        assertEquals(listOf("sudo dpkg -i $DEB"), steps.lines(StepKind.UPGRADE))
        val text = steps.flatMap { it.commands }.joinToString("\n")
        assertFalse("remove" in text || "purge" in text || " -r " in text || "rm " in text || "/etc/sard" in text, text)
    }

    @Test
    fun `Шаги rpm идут в том же порядке, что шаги deb`() {
        assertEquals(INSTALL_KINDS, install(rpm()).map { it.kind })
    }

    @Test
    fun `Скачивание и проверка суммы rpm устроены как у deb`() {
        val base = "http://sard.corp.example:8080/downloads/agent"
        assertEquals(
            listOf("curl -fsSLO $base/$RPM", "curl -fsSLO $base/SHA256SUMS", "curl -fsSLO $base/SHA256SUMS.minisig"),
            install(rpm()).lines(StepKind.DOWNLOAD),
        )
        assertEquals(listOf("grep '  $RPM\$' SHA256SUMS | sha256sum -c -"), install(rpm()).lines(StepKind.CHECKSUM))
    }

    @Test
    fun `Установка rpm ставит локальный файл через rpm Uvh без репозиториев`() {
        assertEquals(listOf("sudo rpm -Uvh $RPM"), install(rpm()).lines(StepKind.INSTALL))
    }

    @Test
    fun `Шаги после установки rpm совпадают с шагами deb`() {
        val after = listOf(StepKind.CONFIGURE, StepKind.ENROLL, StepKind.REPO_INIT, StepKind.START)
        for (kind in after) assertEquals(install(deb()).lines(kind), install(rpm()).lines(kind), kind.name)
    }

    @Test
    fun `Обновление rpm ставит пакет поверх через rpm Uvh без удаления`() {
        val steps = upgrade(rpm())
        val kinds = listOf(StepKind.DOWNLOAD, StepKind.CHECKSUM, StepKind.SIGNATURE, StepKind.UPGRADE)
        assertEquals(kinds, steps.map { it.kind })
        assertEquals(listOf("sudo rpm -Uvh $RPM"), steps.lines(StepKind.UPGRADE))
        val text = steps.flatMap { it.commands }.joinToString("\n")
        assertFalse("rpm -e" in text || "dnf" in text || "yum" in text || "/etc/sard" in text, text)
    }

    @Test
    fun `Обновление без подписи не даёт шага подписи`() {
        assertEquals(
            listOf(StepKind.DOWNLOAD, StepKind.CHECKSUM, StepKind.UPGRADE),
            upgrade(deb(signed = false)).map { it.kind },
        )
    }

    @Test
    fun `Обновление из архива заменяет бинарники и перезапускает работающую службу`() {
        val dir = "sard-agent_v1.4.0_linux_arm64"
        val steps = upgrade(tar())
        assertEquals(
            listOf(StepKind.DOWNLOAD, StepKind.CHECKSUM, StepKind.SIGNATURE, StepKind.UPGRADE, StepKind.RESTART),
            steps.map { it.kind },
        )
        assertEquals(
            listOf("tar -xzf $TAR", "sudo install -m 0755 $dir/sard-agent $dir/restic /usr/libexec/sard/"),
            steps.lines(StepKind.UPGRADE),
        )
        assertEquals(listOf("sudo systemctl try-restart sard-agent.service"), steps.lines(StepKind.RESTART))
        assertFalse(steps.flatMap { it.commands }.any { "/etc/sard" in it })
    }
}

/** The programs a shell line runs: the words that open a command, through pipes, lists, `sudo` and `sh -c '...'`. */
private fun utilities(line: String): Set<String> {
    val nested = Regex("""sh -c '(.*)'$""").find(line)
    val outer = nested?.let { line.removeRange(it.range) + "sh" } ?: line
    val inner = nested?.let { utilities(it.groupValues[1]) }.orEmpty()
    val own =
        outer
            .replace(Regex(""""[^"]*""""), "\"\"")
            .split(Regex("""\s*(\|\||&&|\||;)\s*"""))
            .map { it.trim().removePrefix("{").trim() }
            .filter { it.isNotEmpty() && it != "}" && !it.startsWith("[") }
            .flatMap(::wordsOf)
    return (own + inner).toSet()
}

private fun wordsOf(command: String): List<String> {
    val words = command.split(' ')
    if (words[0] != "sudo") return listOf(words[0])
    val rest = words.drop(1).let { if (it.firstOrNull() == "-u") it.drop(2) else it }
    return listOf("sudo") + wordsOf(rest.joinToString(" "))
}
