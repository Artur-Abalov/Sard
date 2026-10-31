// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package org.springframework.fake

import org.springframework.boot.test.context.TestConfiguration
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RestController

/** A controller of an "extension" that dresses its package as the framework's (CORE1 R27). */
@TestConfiguration(proxyBeanMethods = false)
@RestController
class FakeFrameworkController {
    @GetMapping("/fake/ping")
    fun ping(): String = System.getProperty("user.name", "pong")
}
