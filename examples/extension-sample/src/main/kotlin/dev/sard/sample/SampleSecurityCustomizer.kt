// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.Operation
import dev.sard.server.extension.SardSecurityCustomizer
import org.springframework.security.config.annotation.web.builders.HttpSecurity

/**
 * The sample's entry into the one security chain of the core (CORE1 Р41). It declares public operations only when a
 * property asks for it: `sard.sample.public-ping` opens `GET /api/v1/sample/ping` without a session;
 * `sard.sample.public-outside` declares `GET /sample/open`, outside /api/v1, which the core refuses at start (Р44).
 * A real sign-in (SSO) would add its filter in [customize]; the sample has none, so it adds nothing to the chain.
 */
class SampleSecurityCustomizer(
    private val properties: SampleProperties,
) : SardSecurityCustomizer {
    override val publicOperations: Set<Operation>
        get() =
            buildSet {
                if (properties.publicPing) add(Operation("GET", "/api/v1/sample/ping"))
                if (properties.publicOutside) add(Operation("GET", "/sample/open"))
            }

    override fun customize(http: HttpSecurity) = Unit
}
