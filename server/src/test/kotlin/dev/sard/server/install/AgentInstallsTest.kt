// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.downloads.AgentArtifact
import dev.sard.server.downloads.AgentManifest
import dev.sard.server.downloads.AgentOffer
import dev.sard.server.downloads.AgentPackageCatalog
import dev.sard.server.enrollment.AgentEndpoint
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val KEY = ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d")
private val SHA = "a".repeat(64)

private fun artifact(
    arch: String,
    format: String,
    ext: String,
) = AgentArtifact("sard-agent_v1.4.0_linux_$arch.$ext", 1, SHA, arch, format)

private val ARTIFACTS =
    listOf(
        artifact("amd64", "deb", "deb"),
        artifact("arm64", "deb", "deb"),
        artifact("amd64", "tar.gz", "tar.gz"),
        artifact("arm64", "tar.gz", "tar.gz"),
        artifact("amd64", "rpm", "rpm"),
    )

private fun catalog(
    signed: Boolean = true,
    artifacts: List<AgentArtifact> = ARTIFACTS,
): AgentPackageCatalog {
    val metadata = mapOf("manifest.json" to SHA, "SHA256SUMS" to SHA)
    val sig = if (signed) mapOf("SHA256SUMS.minisig" to SHA) else emptyMap()
    return AgentPackageCatalog.of(AgentManifest(1, "v1.4.0", "0.19.1", artifacts), "v1.4.0", metadata + sig)
}

private fun installs(offer: AgentOffer) =
    AgentInstalls(
        offer,
        InstallCommands(
            DownloadsUrl.resolve("", AgentEndpoint("sard.corp.example:9090"), 8080),
            AgentEndpoint("sard.corp.example:9090"),
            KEY,
        ),
    )

/** The rules of the install block that do not depend on the HTTP layer: what the release and the setting give. */
@MutFlowTest
class AgentInstallsTest {
    @Test
    fun `Выбор rpm берёт имя файла из манифеста, а не строит его`() {
        val rpm = AgentArtifact("sard-agent-v1.4.0.aarch64.rpm", 1, SHA, "arm64", "rpm")
        val offer = AgentOffer.Serving(catalog(artifacts = listOf(rpm)))
        val info = MutFlow.underTest { installs(offer).install(InstallArch.ARM64, InstallFormat.RPM, FetchTool.CURL) }
        assertEquals("sard-agent-v1.4.0.aarch64.rpm", downloadedPackage(info))
        assertEquals(StepKind.INSTALL, info.steps[3].kind)
    }

    @Test
    fun `Выбор rpm без rpm в релизе даёт пустой список шагов`() {
        val info = install(arch = InstallArch.ARM64, format = InstallFormat.RPM)
        assertEquals(true, info.downloadsEnabled)
        assertEquals(emptyList(), info.steps)
    }

    @Test
    fun `Обновление rpm берёт архитектуру агента, а без rpm в релизе даёт arch_unavailable`() {
        val info = upgrade("amd64", format = InstallFormat.RPM)
        assertEquals("sard-agent_v1.4.0_linux_amd64.rpm", downloadedPackage(info))
        assertNull(info.reason)

        val missing = upgrade("arm64", format = InstallFormat.RPM)
        assertEquals(emptyList(), missing.steps)
        assertEquals(UpgradeReason.ARCH_UNAVAILABLE, missing.reason)
    }

    private val serving = installs(AgentOffer.Serving(catalog()))
    private val withheld = installs(AgentOffer.Withheld("v1.4.0"))

    private fun install(
        installs: AgentInstalls = serving,
        arch: InstallArch = InstallArch.AMD64,
        format: InstallFormat = InstallFormat.DEB,
    ) = MutFlow.underTest { installs.install(arch, format, FetchTool.CURL) }

    private fun upgrade(
        arch: String?,
        installs: AgentInstalls = serving,
        format: InstallFormat = InstallFormat.DEB,
    ) = MutFlow.underTest { installs.upgrade(arch, format, FetchTool.CURL) }

    private fun downloadedPackage(info: InstallInfo) =
        info.steps
            .single { it.kind == StepKind.DOWNLOAD }
            .commands
            .first()
            .substringAfterLast('/')

    @Test
    fun `Ответ называет версию агента и restic из манифеста`() {
        val info = install()
        assertEquals(true, info.downloadsEnabled)
        assertEquals("v1.4.0", info.agentVersion)
        assertEquals("0.19.1", info.resticVersion)
        assertEquals(true, info.signed)
        assertNull(info.reason)
    }

    @Test
    fun `Шаги установки подписанного релиза - с шагом подписи, он один необязателен`() {
        val info = install()

        assertEquals(
            listOf("download", "checksum", "signature", "install", "configure", "enroll", "repo-init", "start"),
            info.steps.map {
                it.kind.name
                    .lowercase()
                    .replace('_', '-')
            },
        )
        assertEquals(listOf(StepKind.SIGNATURE), info.steps.filter { it.optional }.map { it.kind })
        assertEquals(
            3,
            info.steps
                .first()
                .commands.size,
        )
    }

    @Test
    fun `Утилита скачивания - curl или wget, как выбрано`() {
        val curl = MutFlow.underTest { serving.install(InstallArch.AMD64, InstallFormat.DEB, FetchTool.CURL) }
        val wget = MutFlow.underTest { serving.install(InstallArch.AMD64, InstallFormat.DEB, FetchTool.WGET) }

        assertEquals(
            true,
            curl.steps
                .first()
                .commands
                .all { it.startsWith("curl ") },
        )
        assertEquals(
            true,
            wget.steps
                .first()
                .commands
                .all { it.startsWith("wget ") },
        )
    }

    @Test
    fun `Без параметров скачивается deb для amd64, а выбор arm64 и архива даёт архив для arm64`() {
        assertEquals("sard-agent_v1.4.0_linux_amd64.deb", downloadedPackage(install()))
        val tar = install(arch = InstallArch.ARM64, format = InstallFormat.TAR)
        assertEquals("sard-agent_v1.4.0_linux_arm64.tar.gz", downloadedPackage(tar))
    }

    @Test
    fun `Неподписанный релиз помечен и не даёт шага подписи`() {
        val info = install(installs(AgentOffer.Serving(catalog(signed = false))))
        assertEquals(false, info.signed)
        assertEquals(false, info.steps.any { it.kind == StepKind.SIGNATURE })
        assertEquals(
            2,
            info.steps
                .first()
                .commands.size,
        )
    }

    @Test
    fun `Сочетание без пакета в релизе даёт пустой список шагов`() {
        val amd64Only = installs(AgentOffer.Serving(catalog(artifacts = ARTIFACTS.take(1))))
        val info = install(amd64Only, arch = InstallArch.ARM64, format = InstallFormat.DEB)
        assertEquals(true, info.downloadsEnabled)
        assertEquals(emptyList(), info.steps)
    }

    @Test
    fun `При выключенной раздаче шагов нет, версия агента - версия сервера, restic неизвестен`() {
        val info = install(withheld)
        assertEquals(false, info.downloadsEnabled)
        assertEquals(emptyList(), info.steps)
        assertEquals("v1.4.0", info.agentVersion)
        assertNull(info.resticVersion)
        assertEquals(false, info.signed)
    }

    @Test
    fun `Обновление берёт архитектуру агента`() {
        val info = upgrade("arm64")
        assertEquals("sard-agent_v1.4.0_linux_arm64.deb", downloadedPackage(info))
        val kinds = listOf(StepKind.DOWNLOAD, StepKind.CHECKSUM, StepKind.SIGNATURE, StepKind.UPGRADE)
        assertEquals(kinds, info.steps.map { it.kind })
        assertNull(info.reason)
    }

    @Test
    fun `Агент на архитектуре без пакета получает объяснение вместо команд`() {
        for (arch in listOf("386", "riscv64", "arm")) {
            val info = upgrade(arch)
            assertEquals(emptyList(), info.steps)
            assertEquals(UpgradeReason.ARCH_UNAVAILABLE, info.reason)
        }
    }

    @Test
    fun `Агент без Register не получает команд обновления`() {
        val info = upgrade(null)
        assertEquals(emptyList(), info.steps)
        assertEquals(UpgradeReason.ARCH_UNKNOWN, info.reason)
        assertEquals(true, info.downloadsEnabled)
    }

    @Test
    fun `При выключенной раздаче обновление не даёт команд и причины`() {
        for (arch in listOf("amd64", null, "386")) {
            val info = upgrade(arch, withheld)
            assertEquals(false, info.downloadsEnabled)
            assertEquals(emptyList(), info.steps)
            assertNull(info.reason)
        }
    }

    @Test
    fun `Обновление deb и rpm сохраняет конфигурацию и ключи, а из архива не обещает`() {
        assertEquals(true, upgrade("amd64", format = InstallFormat.DEB).keepsConfiguration)
        assertEquals(true, upgrade("amd64", format = InstallFormat.RPM).keepsConfiguration)
        assertEquals(false, upgrade("amd64", format = InstallFormat.TAR).keepsConfiguration)
    }

    @Test
    fun `Признак сохранения конфигурации не зависит от архитектуры`() {
        val full = installs(AgentOffer.Serving(catalog(artifacts = ARTIFACTS + artifact("arm64", "rpm", "rpm"))))
        val flags = InstallFormat.entries.map { upgrade("arm64", full, it).keepsConfiguration }
        assertEquals(listOf(true, true, false), flags)
    }

    @Test
    fun `Обновление без rpm в релизе для архитектуры агента не обещает сохранить конфигурацию`() {
        val info = upgrade("arm64", format = InstallFormat.RPM)
        assertEquals(UpgradeReason.ARCH_UNAVAILABLE, info.reason)
        assertEquals(false, info.keepsConfiguration)
    }

    @Test
    fun `Обновление без команд не обещает сохранить конфигурацию при любом формате`() {
        for (format in InstallFormat.entries) {
            assertEquals(false, upgrade("386", format = format).keepsConfiguration, "arch_unavailable $format")
            assertEquals(false, upgrade(null, format = format).keepsConfiguration, "arch_unknown $format")
            assertEquals(false, upgrade("amd64", withheld, format).keepsConfiguration, "downloads off $format")
        }
    }
}
