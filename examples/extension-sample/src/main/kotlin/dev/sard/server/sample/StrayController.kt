// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.sample

import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

/**
 * `sard.sample.stray-controller = true`: a controller outside /api/v1 in a package of the core's namespace
 * (`dev.sard.server.*`), which the application of the core finds by its component scan. The core must still see it
 * is not its own and refuse the start (CORE1 Р27). The class is in this package on purpose; without the property the
 * scan skips it.
 */
@RestController
@ConditionalOnProperty("sard.sample.stray-controller", havingValue = "true")
class StrayController {
    @GetMapping("/sample/stray")
    fun stray(): String = System.getProperty("user.name", "stray")
}
