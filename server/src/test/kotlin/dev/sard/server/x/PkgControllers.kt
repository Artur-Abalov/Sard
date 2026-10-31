// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.x

import org.springframework.boot.test.context.TestConfiguration
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

/**
 * A controller of an "extension" that lives in a package of the core's namespace (CORE1 Р27). `@TestConfiguration`
 * keeps the component scan of the server's tests from picking it up.
 */
@TestConfiguration(proxyBeanMethods = false)
@RestController
class PkgOutsideController {
    @GetMapping("/ext/ping")
    fun ping(): String = System.getProperty("user.name", "pong")
}

@TestConfiguration(proxyBeanMethods = false)
@RestController
class PkgInsideController {
    @GetMapping("/api/v1/pkg-inside/ping")
    fun ping(): String = System.getProperty("user.name", "pong")
}
