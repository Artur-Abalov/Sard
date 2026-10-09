// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.onboarding.ServerCommand
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.runApplication
import kotlin.system.exitProcess

@SpringBootApplication
class SardServerApplication

/** A command as the first argument (docker compose run --rm server admin-reset) runs and exits; otherwise the server. */
fun main(args: Array<String>) {
    ServerCommand().run(args, System.getenv(), System.out, System.err)?.let { exitProcess(it) }
    runApplication<SardServerApplication>(*args)
}
