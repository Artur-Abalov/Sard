// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.info.Info
import io.swagger.v3.oas.models.info.License
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

/** OpenAPI metadata; the web client is generated from /v3/api-docs. */
@Configuration(proxyBeanMethods = false)
class OpenApiConfiguration {
    @Bean
    fun sardOpenApi(): OpenAPI =
        OpenAPI().info(
            Info()
                .title("Sard API")
                .version("v1")
                .license(License().name("AGPL-3.0-only").identifier("AGPL-3.0-only")),
        )
}
