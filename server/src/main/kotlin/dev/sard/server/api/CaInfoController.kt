// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.pki.CertificateAuthority
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController

@Schema(description = "The CA of this server; public data, but only for a signed-in administrator")
data class CaInfo(
    @field:Schema(
        description =
            "SHA-256 of the root's DER SubjectPublicKeyInfo, 64 lower-case hex characters: the part of an " +
                "enrollment token after the last dot",
        example = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f",
    )
    val fingerprint: String,
)

/** The fingerprint of the server's CA, for comparing a moved server with the old one (ADR 0052). Never the key. */
@RestController
@RequestMapping("/api/v1/ca", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "ca")
class CaInfoController(
    private val ca: CertificateAuthority,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "CA of the server",
        description = "The fingerprint of the CA, whatever its origin. Requires an administrator session.",
    )
    fun info(): CaInfo = CaInfo(ca.fingerprint().hex)
}
