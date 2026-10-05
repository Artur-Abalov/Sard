// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.media.Schema

/** The architectures the release has packages for (GOARCH). */
@Schema(enumAsRef = true)
enum class InstallArch(
    /** The architecture as the agent reports it in Register and manifest.json names it. */
    val goarch: String,
) {
    @JsonProperty("amd64")
    AMD64("amd64"),

    @JsonProperty("arm64")
    ARM64("arm64"),
}

/** What the console offers: a deb package or the tar.gz archive. RPM stays in the release but is not offered. */
@Schema(enumAsRef = true)
enum class InstallFormat(
    /** The name of the format in manifest.json. */
    val manifestName: String,
) {
    @JsonProperty("deb")
    DEB("deb"),

    @JsonProperty("tar")
    TAR("tar.gz"),
}

/** The tool the commands download with. */
@Schema(enumAsRef = true)
enum class FetchTool {
    @JsonProperty("curl")
    CURL,

    @JsonProperty("wget")
    WGET,
}

/** What a step of the install or upgrade block does; the console turns it into a title and an explanation. */
@Schema(enumAsRef = true)
enum class StepKind {
    @JsonProperty("download")
    DOWNLOAD,

    @JsonProperty("checksum")
    CHECKSUM,

    @JsonProperty("signature")
    SIGNATURE,

    @JsonProperty("install")
    INSTALL,

    @JsonProperty("configure")
    CONFIGURE,

    @JsonProperty("enroll")
    ENROLL,

    @JsonProperty("repo-init")
    REPO_INIT,

    @JsonProperty("start")
    START,

    @JsonProperty("upgrade")
    UPGRADE,

    @JsonProperty("restart")
    RESTART,
}

/** Why an upgrade block has no commands. */
@Schema(enumAsRef = true)
enum class UpgradeReason {
    @JsonProperty("arch_unknown")
    ARCH_UNKNOWN,

    @JsonProperty("arch_unavailable")
    ARCH_UNAVAILABLE,
}

@Schema(description = "One step of the block: shell commands to run in order, as one unit")
data class InstallStep(
    val kind: StepKind,
    @field:Schema(description = "Shell commands of the step, the same for every language of the console")
    val commands: List<String>,
    @field:Schema(description = "True when the step may be skipped (the signature check)")
    val optional: Boolean,
)
