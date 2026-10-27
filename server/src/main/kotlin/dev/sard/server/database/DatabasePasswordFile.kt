// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import org.springframework.boot.EnvironmentPostProcessor
import org.springframework.boot.SpringApplication
import org.springframework.core.env.ConfigurableEnvironment
import org.springframework.core.env.MapPropertySource

private const val VARIABLE = "SARD_DB_PASSWORD"

/**
 * Reads the password of the server's database role from the file `SARD_DB_PASSWORD_FILE` names, when it is set (DB2):
 * the file is in a volume of db-init, the variable would be in `docker inspect`. A file that cannot be read stops the
 * start and the message names the variable.
 */
class DatabasePasswordFile : EnvironmentPostProcessor {
    override fun postProcessEnvironment(
        environment: ConfigurableEnvironment,
        application: SpringApplication,
    ) {
        val file = environment.getProperty("${VARIABLE}_FILE")?.takeIf { it.isNotBlank() } ?: return
        val password = PasswordSource.resolve(mapOf("${VARIABLE}_FILE" to file), VARIABLE)
        val property = mapOf("spring.datasource.password" to password)
        environment.propertySources.addFirst(MapPropertySource("sardDatabasePasswordFile", property))
    }
}
