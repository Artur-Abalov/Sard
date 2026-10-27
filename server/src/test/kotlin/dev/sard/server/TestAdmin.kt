// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import org.testcontainers.postgresql.PostgreSQLContainer
import java.util.concurrent.ConcurrentHashMap

/**
 * The superuser of a test cluster (DB2). The server of a test connects as sard_app, which may not truncate a table,
 * change the schema or touch a record of a verification; a test that must goes through the superuser of the cluster
 * its server's database is in, found by the address of that database.
 */
object TestAdmin {
    private val logins = ConcurrentHashMap<String, Pair<String, String>>()

    private fun key(jdbcUrl: String) = jdbcUrl.substringAfter("jdbc:postgresql://").substringBefore('/')

    fun register(container: PostgreSQLContainer) {
        logins["${container.host}:${container.firstMappedPort}"] = container.username to container.password
    }

    /** A template of the superuser for the database [jdbc] is connected to. */
    fun of(jdbc: JdbcTemplate): JdbcTemplate {
        val url = checkNotNull(jdbc.dataSource).connection.use { it.metaData.url }
        val (user, password) = checkNotNull(logins[key(url)]) { "no test cluster at $url" }
        val source = DriverManagerDataSource(url, user, password)
        // The superuser has no search_path of Sard's (that is a role setting of Sard's roles): its tables are in sard.
        source.setConnectionProperties(java.util.Properties().apply { setProperty("currentSchema", "sard") })
        return JdbcTemplate(source)
    }
}
