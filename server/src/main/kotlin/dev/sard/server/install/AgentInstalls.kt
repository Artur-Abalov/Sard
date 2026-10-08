// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.downloads.AgentOffer

/** What the console shows to install or upgrade an agent: versions, whether the release is signed, the steps. */
data class InstallInfo(
    val downloadsEnabled: Boolean,
    val agentVersion: String,
    val resticVersion: String?,
    val signed: Boolean,
    val steps: List<InstallStep>,
    /** Why an upgrade has no steps although downloads are on; always null for an install. */
    val reason: UpgradeReason? = null,
    /** Whether the upgrade keeps the configuration and keys; false when there are no steps to promise it for. */
    val keepsConfiguration: Boolean = false,
)

/**
 * The install and upgrade blocks of U1b, decided here so the console only shows them: which package
 * a host needs, whether there is a signature step, what the console says when there is nothing to run.
 */
class AgentInstalls(
    private val offer: AgentOffer,
    private val commands: InstallCommands,
) {
    fun install(
        arch: InstallArch,
        format: InstallFormat,
        fetch: FetchTool,
    ): InstallInfo =
        when (offer) {
            is AgentOffer.Withheld -> {
                withheld(offer)
            }

            is AgentOffer.Serving -> {
                val file = offer.catalog.fileOf(arch.goarch, format.manifestName)
                val steps = file?.let { commands.install(releasePackage(offer, it, format, fetch)) }.orEmpty()
                serving(offer, steps, null)
            }
        }

    /** [arch] is what the agent reported in its last Register, null if it never did. */
    fun upgrade(
        arch: String?,
        format: InstallFormat,
        fetch: FetchTool,
    ): InstallInfo =
        when (offer) {
            is AgentOffer.Withheld -> {
                withheld(offer)
            }

            is AgentOffer.Serving -> {
                val file = arch?.let { offer.catalog.fileOf(it, format.manifestName) }
                val steps = file?.let { commands.upgrade(releasePackage(offer, it, format, fetch)) }.orEmpty()
                val reason = if (file == null) missing(arch) else null
                serving(offer, steps, reason, keeps(format, steps))
            }
        }

    private fun keeps(
        format: InstallFormat,
        steps: List<InstallStep>,
    ) = format.keepsConfiguration && steps.isNotEmpty()

    private fun missing(arch: String?) =
        when (arch) {
            null -> UpgradeReason.ARCH_UNKNOWN
            else -> UpgradeReason.ARCH_UNAVAILABLE
        }

    private fun releasePackage(
        offer: AgentOffer.Serving,
        file: String,
        format: InstallFormat,
        fetch: FetchTool,
    ) = ReleasePackage(file, format, offer.catalog.signed, fetch)

    private fun serving(
        offer: AgentOffer.Serving,
        steps: List<InstallStep>,
        reason: UpgradeReason?,
        keepsConfiguration: Boolean = false,
    ) = InstallInfo(
        true,
        offer.version,
        offer.catalog.resticVersion,
        offer.catalog.signed,
        steps,
        reason,
        keepsConfiguration,
    )

    private fun withheld(offer: AgentOffer.Withheld) = InstallInfo(false, offer.version, null, false, emptyList())
}
