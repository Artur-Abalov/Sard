// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean
import java.time.Clock

/** The server's time source; tests replace it with a fixed clock. */
@AutoConfiguration
class ClockAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean
    fun clock(): Clock = Clock.systemUTC()
}
