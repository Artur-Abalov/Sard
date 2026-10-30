// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.testext.outside

import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

@RestController
class OutsideController {
    @GetMapping("/ext/ping")
    fun ping(): String = System.getProperty("user.name", "pong")
}

@RestController
class InsideController {
    @GetMapping("/api/v1/ext/ping")
    fun ping(): String = System.getProperty("user.name", "pong")
}
