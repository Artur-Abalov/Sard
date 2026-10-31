// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.boot.autoconfigure.AutoConfigurationImportFilter
import org.springframework.boot.autoconfigure.AutoConfigurationMetadata

private const val GRPC_SECURITY = "org.springframework.boot.grpc.server.autoconfigure.security"

/** The default user with a generated password, and the security of Spring gRPC (CORE1 Р34, Р45, ADR 0071). */
private val NOT_IMPORTED =
    setOf(
        "org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration",
        "$GRPC_SECURITY.GrpcServerSecurityAutoConfiguration",
        "$GRPC_SECURITY.GrpcServerOAuth2ResourceServerAutoConfiguration",
    )

/**
 * Keeps three auto-configurations of Spring Boot out of every application built on the core: the default user (the
 * chain of the core has no users), and the gRPC security, whose interceptors would stand beside the mTLS check of the
 * agents on port 9090. It is a filter of the imports, not a property: what an operator excludes in
 * `spring.autoconfigure.exclude`, in any form, stays his.
 */
class SecurityAutoConfigurationFilter : AutoConfigurationImportFilter {
    override fun match(
        autoConfigurationClasses: Array<String?>,
        autoConfigurationMetadata: AutoConfigurationMetadata,
    ): BooleanArray = BooleanArray(autoConfigurationClasses.size) { autoConfigurationClasses[it] !in NOT_IMPORTED }
}
