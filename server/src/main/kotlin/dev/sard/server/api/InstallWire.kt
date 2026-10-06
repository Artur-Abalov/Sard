// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.media.Schema

// The wire twins of the install package's types (U1b). Their names are the schema names of the
// OpenAPI document; the constants are those of the domain enums, which AgentInstallApiImpl maps by name.

/** The architectures the release has packages for (GOARCH). */
@Schema(enumAsRef = true)
enum class InstallArch {
    @JsonProperty("amd64")
    AMD64,

    @JsonProperty("arm64")
    ARM64,
}

/** What the console offers: a deb package or the tar.gz archive. RPM stays in the release but is not offered. */
@Schema(enumAsRef = true)
enum class InstallFormat {
    @JsonProperty("deb")
    DEB,

    @JsonProperty("tar")
    TAR,
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

/** The key the packages are signed with. */
data class ReleaseKey(
    /** The key ID minisign prints, 16 hex digits. */
    val id: String,
    /** The second line of the key file, the string `minisign -P` takes. */
    val publicKey: String,
)
