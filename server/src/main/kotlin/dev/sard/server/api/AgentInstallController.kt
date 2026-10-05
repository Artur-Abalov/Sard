// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.install.FetchTool
import dev.sard.server.install.InstallArch
import dev.sard.server.install.InstallFormat
import dev.sard.server.install.InstallStep
import dev.sard.server.install.ReleaseKey
import dev.sard.server.install.UpgradeReason
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.util.UUID

private const val DOWNLOADS_ENABLED =
    "False when the server hands out no packages (SARD_AGENT_DOWNLOADS=false): there are no steps then"
private const val AGENT_VERSION = "Version of the packages the server hands out; its own version when it hands out none"
private const val RESTIC_VERSION = "Version of the restic in those packages; null when downloads are off"
private const val SIGNED = "Whether the release has SHA256SUMS.minisig; without it there is no signature step"
private const val RELEASE_KEY =
    "The key the packages are signed with, from the server build: the console tells to compare it with README.md"
private const val MANUAL_DOC = "Link to the documentation of the manual installation, for when there are no steps"
private const val STEPS = "Steps in the order to run them; empty when there is nothing to run"

@Schema(description = "How to install an agent on a new host: versions and the commands, step by step")
data class AgentInstall(
    @field:Schema(description = DOWNLOADS_ENABLED)
    val downloadsEnabled: Boolean,
    @field:Schema(description = AGENT_VERSION)
    val agentVersion: String,
    @field:Schema(description = RESTIC_VERSION)
    val resticVersion: String?,
    val arch: InstallArch,
    val format: InstallFormat,
    @field:Schema(description = SIGNED)
    val signed: Boolean,
    @field:Schema(description = RELEASE_KEY)
    val releaseKey: ReleaseKey,
    @field:Schema(description = MANUAL_DOC)
    val manualInstallDoc: String,
    @field:Schema(
        description = "$STEPS; kinds: download, checksum, signature, install, configure, enroll, repo-init, start",
    )
    val steps: List<InstallStep>,
)

@Schema(description = "How to upgrade one agent: the same as an install, for the architecture of its last Register")
data class AgentUpgrade(
    @field:Schema(description = DOWNLOADS_ENABLED)
    val downloadsEnabled: Boolean,
    @field:Schema(description = AGENT_VERSION)
    val agentVersion: String,
    @field:Schema(description = RESTIC_VERSION)
    val resticVersion: String?,
    @field:Schema(description = "GOARCH of the agent's last Register; null if it never sent one")
    val arch: String?,
    val format: InstallFormat,
    @field:Schema(description = SIGNED)
    val signed: Boolean,
    @field:Schema(description = RELEASE_KEY)
    val releaseKey: ReleaseKey,
    @field:Schema(description = MANUAL_DOC)
    val manualInstallDoc: String,
    @field:Schema(description = STEPS + "; kinds: download, checksum, signature, upgrade, restart")
    val steps: List<InstallStep>,
    @field:Schema(description = "Why there are no steps although downloads are on; null otherwise")
    val reason: UpgradeReason?,
)

/** What the install endpoints do; [AgentInstallApiImpl] implements it. */
interface AgentInstallApi {
    fun agentInstall(
        arch: InstallArch,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentInstall

    fun agentUpgrade(
        agentId: UUID,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentUpgrade
}

@RestController
@RequestMapping("/api/v1", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "agent-install")
class AgentInstallController(
    private val api: AgentInstallApi,
) {
    @GetMapping("/agent-install")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Install an agent",
        description =
            "Versions and shell commands, step by step, to install an agent from this server's packages. " +
                "The enroll step holds a placeholder: a token string is never in the answer.",
    )
    @Unprocessable
    fun agentInstall(
        @RequestParam(defaultValue = "amd64") arch: InstallArch,
        @RequestParam(defaultValue = "deb") format: InstallFormat,
        @RequestParam(defaultValue = "curl") fetch: FetchTool,
    ): AgentInstall = api.agentInstall(arch, format, fetch)

    @GetMapping("/agents/{agentId}/upgrade")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Upgrade an agent",
        description = "The commands to bring an installed agent to the version of this server's packages.",
    )
    @NotFound
    @Unprocessable
    fun agentUpgrade(
        @PathVariable agentId: UUID,
        @RequestParam(defaultValue = "deb") format: InstallFormat,
        @RequestParam(defaultValue = "curl") fetch: FetchTool,
    ): AgentUpgrade = api.agentUpgrade(agentId, format, fetch)
}
