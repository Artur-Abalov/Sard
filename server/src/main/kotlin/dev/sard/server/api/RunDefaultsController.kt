// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController

private const val BACKUP = "The step timeout of a backup in minutes, 1 to 1440, for a source that names none"
private const val RESTORE = "The step timeout of a restore in minutes, 1 to 1440, for a source that names none"

@Schema(description = "The default step timeouts of the installation (J1b); a run takes them when it is created")
data class RunDefaults(
    @field:Schema(description = BACKUP, minimum = "1", maximum = "1440")
    val backupTimeoutMinutes: Int,
    @field:Schema(description = RESTORE, minimum = "1", maximum = "1440")
    val restoreTimeoutMinutes: Int,
)

/** What the run-defaults endpoints do. */
interface RunDefaultsApi {
    fun getRunDefaults(): RunDefaults

    fun setRunDefaults(input: RunDefaults): RunDefaults
}

@RestController
@RequestMapping("/api/v1/run-defaults", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "run-defaults")
class RunDefaultsController(
    private val api: RunDefaultsApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "The default step timeouts",
        description = "6 h for a backup and 12 h for a restore until set. A source's own timeout overrides them.",
    )
    fun getRunDefaults(): RunDefaults = api.getRunDefaults()

    @PutMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Set the default step timeouts",
        description = "Both are required. They count for runs created after the change; runs in flight keep theirs.",
    )
    @Unprocessable
    fun setRunDefaults(
        @RequestBody input: RunDefaults,
    ): RunDefaults = api.setRunDefaults(input)
}
