// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.scheduler.PreviewedSchedule
import dev.sard.server.scheduler.ScheduleLanguage
import dev.sard.server.scheduler.SchedulePreviews
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.stereotype.Component
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant

@Schema(description = "A schedule as it would be saved, in words, with its nearest fires; nothing is stored")
data class SchedulePreview(
    @field:Schema(description = "Five fields, single-spaced, as PUT schedule would store it")
    val cron: String,
    @field:Schema(description = "The zone the fires are read in: the requested one, or the server's")
    val timezone: String,
    @field:Schema(description = "The schedule in words in the language asked for")
    val description: String,
    @field:Schema(description = "The three nearest fires strictly after now")
    val nextFires: List<Instant>,
    @field:Schema(description = "Two of the next 100 fires are closer than 15 minutes: a warning, not a ban")
    val tooFrequent: Boolean,
)

/** What the preview endpoint does (F3b). */
interface SchedulePreviewApi {
    fun preview(
        cron: String,
        timezone: String?,
        lang: String?,
    ): SchedulePreview
}

@RestController
@RequestMapping("/api/v1/schedule-preview", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "sources")
class SchedulePreviewController(
    private val api: SchedulePreviewApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Preview a schedule before saving it",
        description =
            "The same checks as PUT schedule: 422 validation_failed names cron, timezone or lang. Without timezone " +
                "the server's zone is used; lang is ru or en (en by default).",
    )
    @Unprocessable
    fun preview(
        @RequestParam(defaultValue = "") cron: String,
        @RequestParam(required = false) timezone: String?,
        @RequestParam(required = false) lang: String?,
    ): SchedulePreview = api.preview(cron, timezone, lang)
}

/** The preview over [SchedulePreviews]. */
@Component
class SchedulePreviewApiImpl(
    private val previews: SchedulePreviews,
) : SchedulePreviewApi {
    override fun preview(
        cron: String,
        timezone: String?,
        lang: String?,
    ): SchedulePreview = preview(previews.of(cron, timezone, language(lang)))

    private fun language(lang: String?): ScheduleLanguage =
        when (lang) {
            null, "en" -> ScheduleLanguage.EN
            "ru" -> ScheduleLanguage.RU
            else -> throw RequestInvalid("lang", "must be ru or en")
        }

    private fun preview(view: PreviewedSchedule) =
        SchedulePreview(view.cron, view.timezone, view.description, view.nextFires, view.tooFrequent)
}
