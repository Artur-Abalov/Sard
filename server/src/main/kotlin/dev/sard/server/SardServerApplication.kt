// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.onboarding.ServerCommand
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication
import org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration
import kotlin.system.exitProcess

// No default user and generated password: the chain of the core is the only HTTP security (CORE1 Р34, ADR 0071).
@SpringBootApplication(exclude = [UserDetailsServiceAutoConfiguration::class])
class SardServerApplication

/**
 * A command as the first argument (docker compose run --rm server admin-reset) runs and exits; otherwise the
 * server starts.
 */
fun main(args: Array<String>) {
    ServerCommand().run(args, System.getenv(), System.out, System.err)?.let { exitProcess(it) }
    runApplication<SardServerApplication>(*args)
}
