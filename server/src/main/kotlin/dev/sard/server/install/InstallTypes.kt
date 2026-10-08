// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

/** The architectures the release has packages for (GOARCH). */
enum class InstallArch(
    /** The architecture as the agent reports it in Register and manifest.json names it. */
    val goarch: String,
) {
    AMD64("amd64"),
    ARM64("arm64"),
}

/** What the console offers: a deb package, an rpm package or the tar.gz archive (ADR 0048). */
enum class InstallFormat(
    /** The name of the format in manifest.json. */
    val manifestName: String,
    /** Whether an upgrade in this format leaves /etc/sard (configuration, keys) alone; only deb, rpm are verified. */
    val keepsConfiguration: Boolean,
) {
    DEB("deb", true),
    RPM("rpm", true),
    TAR("tar.gz", false),
}

/** The tool the commands download with. */
enum class FetchTool {
    CURL,
    WGET,
}

/** What a step of the install or upgrade block does; the console turns it into a title and an explanation. */
enum class StepKind {
    DOWNLOAD,
    CHECKSUM,
    SIGNATURE,
    INSTALL,
    CONFIGURE,
    ENROLL,
    REPO_INIT,
    START,
    UPGRADE,
    RESTART,
}

/** Why an upgrade block has no commands. */
enum class UpgradeReason {
    ARCH_UNKNOWN,
    ARCH_UNAVAILABLE,
}

data class InstallStep(
    val kind: StepKind,
    val commands: List<String>,
    val optional: Boolean,
)
