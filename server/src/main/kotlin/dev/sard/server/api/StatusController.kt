// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.verify.RestoreVerifications
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.security.SecurityRequirements
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.boot.info.BuildProperties
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController
import java.time.Instant

/** Server status for the UI and sardctl. */
data class StatusResponse(
    @field:Schema(description = "sard-server version", requiredMode = Schema.RequiredMode.REQUIRED)
    val version: String,
    @field:Schema(
        description = "When a restore was last verified successfully; null if never",
        requiredMode = Schema.RequiredMode.REQUIRED,
    )
    val lastVerifiedRestoreAt: Instant?,
)

@RestController
@RequestMapping("/api/v1")
@Tag(name = "status")
class StatusController(
    private val build: BuildProperties,
    private val verifications: RestoreVerifications,
) {
    @GetMapping("/status", produces = [MediaType.APPLICATION_JSON_VALUE])
    @SecurityRequirements
    @Operation(summary = "Server status", description = "Public: the dashboard shows it before sign-in.")
    fun status(): StatusResponse = StatusResponse(build.version ?: UNKNOWN_VERSION, verifications.lastVerifiedAt())

    private companion object {
        const val UNKNOWN_VERSION = "unknown"
    }
}
