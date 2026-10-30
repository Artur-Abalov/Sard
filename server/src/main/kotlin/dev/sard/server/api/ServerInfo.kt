// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import dev.sard.server.extension.Edition
import dev.sard.server.extension.EditionProvider
import dev.sard.server.extension.ExtensionRegistry
import dev.sard.server.extension.LicenceItem
import dev.sard.server.extension.LicenceState
import dev.sard.server.extension.LicenceSummary
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.slf4j.LoggerFactory
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController
import java.time.Instant

private val log = LoggerFactory.getLogger(ServerInfoController::class.java)

@Schema(description = "The edition of this build")
data class EditionInfo(
    @field:Schema(description = "\"free\" in the open core", requiredMode = Schema.RequiredMode.REQUIRED)
    val id: String,
    val displayName: String,
)

@Schema(description = "An extension on the server's classpath")
data class ExtensionInfo(
    val id: String,
    val displayName: String,
)

/** How the extension that owns the licence says it stands; the core does not judge it. */
@Schema(enumAsRef = true)
enum class LicenceStatus {
    @JsonProperty("valid")
    VALID,

    @JsonProperty("expired")
    EXPIRED,

    @JsonProperty("absent")
    ABSENT,

    @JsonProperty("invalid")
    INVALID,

    @JsonProperty("unavailable")
    UNAVAILABLE,
}

@Schema(description = "A line of the licence, shown as the extension gave it")
data class LicenceItemInfo(
    val label: String,
    val value: String,
)

@Schema(description = "The licence summary of the extension; limits are shown, never applied by the core")
data class LicenceInfo(
    val state: LicenceStatus,
    @field:Schema(description = "null when the extension did not report it")
    val holder: String?,
    @field:Schema(description = "null when the extension did not report it")
    val expiresAt: Instant?,
    val items: List<LicenceItemInfo>,
)

@Schema(description = "What the server tells a signed-in administrator about its edition, extensions and licence")
data class ServerInfo(
    val edition: EditionInfo,
    @field:Schema(description = "Ordered by id; empty in the open core")
    val extensions: List<ExtensionInfo>,
    @field:Schema(description = "null in the free edition")
    val licence: LicenceInfo?,
)

internal fun Edition.toInfo() = EditionInfo(id, displayName)

internal fun LicenceState.toStatus() =
    when (this) {
        LicenceState.VALID -> LicenceStatus.VALID
        LicenceState.EXPIRED -> LicenceStatus.EXPIRED
        LicenceState.ABSENT -> LicenceStatus.ABSENT
        LicenceState.INVALID -> LicenceStatus.INVALID
        LicenceState.UNAVAILABLE -> LicenceStatus.UNAVAILABLE
    }

private fun LicenceItem.toInfo() = LicenceItemInfo(label, value)

private fun LicenceSummary.toInfo() = LicenceInfo(state.toStatus(), holder, expiresAt, items.map { it.toInfo() })

private val UNAVAILABLE_LICENCE = LicenceInfo(LicenceStatus.UNAVAILABLE, null, null, emptyList())

@RestController
@RequestMapping("/api/v1")
@Tag(name = "server")
class ServerInfoController(
    private val editions: EditionProvider,
    private val registry: ExtensionRegistry,
) {
    @GetMapping("/server", produces = [MediaType.APPLICATION_JSON_VALUE])
    @Operation(summary = "Edition, extensions and licence of this server", description = "Needs a session.")
    fun server(): ServerInfo =
        ServerInfo(
            editions.edition().toInfo(),
            registry.extensions.map { ExtensionInfo(it.id, it.displayName) },
            licence(),
        )

    /** A licence seam that throws is no reason to fail the request: the state is "unavailable" and one WARN says why. */
    private fun licence(): LicenceInfo? =
        runCatching { editions.licence()?.toInfo() }
            .getOrElse {
                log.warn("The licence is unavailable: {}", it.message)
                UNAVAILABLE_LICENCE
            }
}
