// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

/** [EnrollmentGrpcService] is the only consumer of the shared IO dispatcher Enroll blocks on. */
@Configuration(proxyBeanMethods = false)
class EnrollmentDispatcherConfiguration {
    /**
     * Enroll blocks on JDBC and signing; injected so tests and callers can choose.
     * `destroyMethod = ""` disables Spring's inferred destroy method: Dispatchers.IO is a
     * process-wide shared instance and cannot be closed, unlike a dispatcher backed by its
     * own executor.
     */
    @Bean(destroyMethod = "")
    fun enrollmentDispatcher(): CoroutineDispatcher = Dispatchers.IO
}
