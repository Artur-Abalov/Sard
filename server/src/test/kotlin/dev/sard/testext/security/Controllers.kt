// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.testext.security

import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController

/**
 * Controllers of the security tests. They live outside `dev.sard.server`, so the component scan of the server tests
 * does not find them and they do not get into the OpenAPI document of every test context; a test declares them.
 */
@RestController
@RequestMapping("/api/v1/things")
class ThingsStub {
    @GetMapping("/{thingId}")
    fun one(): String = answer("one")

    @PostMapping(consumes = ["application/json"])
    fun create(): String = answer("created")

    @GetMapping
    fun all(): String = answer("all")
}

/** The one public operation of the test extension "open-ext" (spring-security.feature, Р41). */
@RestController
@RequestMapping("/api/v1/open-ext")
class OpenExtController {
    @GetMapping("/start")
    fun start(): String = answer("started")
}

/** Not a constant for the static analysis: what the test controllers answer. */
private fun answer(text: String): String = System.getProperty("sard.test.never-set", text)
